package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"

	"frp-desktop/internal/core"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"
	"github.com/egoist/mygo"
)

type GlobalCommand struct {
	OperationID      string
	ExpectedRevision uint64
	Action           Action
}
type SaveAppSettings struct {
	Settings         domain.Settings
	Proxy            *string
	ApplySystemLogin bool
}

func (SaveAppSettings) commandKind() string { return "SaveSettings" }

type InstallCore struct{ Version string }

func (InstallCore) commandKind() string { return "InstallCore" }

type CustomCore struct{ Path string }

func (CustomCore) commandKind() string { return "CustomCore" }

type DeleteCore struct{ ID string }

func (DeleteCore) commandKind() string { return "DeleteCore" }

type SetDefaultCore struct{ ID string }

func (SetDefaultCore) commandKind() string { return "SetDefaultCore" }

type RestoreBackup struct {
	Data     []byte
	Password string
}

func (RestoreBackup) commandKind() string { return "RestoreBackup" }

type ImportCA struct{ Data []byte }

func (ImportCA) commandKind() string { return "ImportCA" }

type ExportBackup struct{ Path, Password string }

func (ExportBackup) commandKind() string { return "ExportBackup" }

type AcknowledgeHideNotice struct{}

func (AcknowledgeHideNotice) commandKind() string { return "AcknowledgeHideNotice" }

func (c *Controller) reserveCommand(ctx context.Context, id string, data []byte) (*commandReceipt, bool, error) {
	mac := hmac.New(sha256.New, c.commandKey[:])
	_, _ = mac.Write(data)
	var fingerprint [32]byte
	copy(fingerprint[:], mac.Sum(nil))
	c.commandMu.Lock()
	prior := c.receipts[id]
	if prior != nil {
		c.commandMu.Unlock()
		if prior.fingerprint != fingerprint {
			return nil, false, domain.Failure(domain.Conflict, "此 operationID 已用于不同的命令，请使用新的操作标识", "operationId", false, nil)
		}
		select {
		case <-ctx.Done():
			return nil, false, commandError(ctx.Err(), domain.Canceled)
		case <-prior.done:
			return prior, true, nil
		}
	}
	receipt := &commandReceipt{fingerprint: fingerprint, done: make(chan struct{})}
	c.receipts[id] = receipt
	c.commandMu.Unlock()
	return receipt, false, nil
}
func globalScope(action Action) string {
	switch action.(type) {
	case SaveAppSettings:
		return "settings"
	case InstallCore, CustomCore, DeleteCore, SetDefaultCore:
		return "cores"
	case RestoreBackup:
		return "restore"
	case ExportBackup:
		return "backup-export"
	case ImportCA:
		return "attachments"
	case AcknowledgeHideNotice:
		return "hide-notice"
	default:
		return ""
	}
}
func (c *Controller) validateGlobal(cmd GlobalCommand, scope string) error {
	state := c.Store.Snapshot()
	revision := uint64(0)
	if scope == "settings" {
		revision = state.Settings.Revision
	} else if scope == "cores" {
		revision = state.CoreRevision
	} else if scope == "backup-export" {
		revision = state.Revision
	}
	if action, ok := cmd.Action.(SaveAppSettings); ok && action.Settings.Revision != cmd.ExpectedRevision {
		return storageConflict()
	}
	if revision != cmd.ExpectedRevision {
		return storageConflict()
	}
	return nil
}
func (c *Controller) ExecuteGlobal(ctx context.Context, cmd GlobalCommand) (CommandResult, error) {
	if !domain.ValidID(cmd.OperationID) || cmd.Action == nil {
		return CommandResult{}, domain.Failure(domain.Validation, "全局命令缺少操作标识或内容", "operationId", false, nil)
	}
	value := reflect.ValueOf(cmd.Action)
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return CommandResult{}, domain.Failure(domain.Validation, "命令内容不能为空", "kind", false, nil)
		}
		cmd.Action = value.Elem().Interface().(Action)
	}
	scope := globalScope(cmd.Action)
	if scope == "" {
		return CommandResult{}, domain.Failure(domain.Validation, "不支持的全局命令", "kind", false, nil)
	}
	data, err := json.Marshal(struct {
		Scope, Kind string
		Command     GlobalCommand
	}{scope, cmd.Action.commandKind(), cmd})
	if err != nil {
		return CommandResult{}, commandError(err, domain.Validation)
	}
	receipt, replayed, err := c.reserveCommand(ctx, cmd.OperationID, data)
	if err != nil {
		return CommandResult{}, err
	}
	if replayed {
		return c.commandReply(receipt.effect, true), receipt.err
	}
	defer close(receipt.done)
	if err = ctx.Err(); err == nil {
		err = c.validateGlobal(cmd, scope)
	}
	var prepared *domain.Core
	if err == nil {
		switch action := cmd.Action.(type) {
		case InstallCore:
			proxy, e := storage.Secret(c.Secrets, c.Store.Snapshot().Settings.DownloadProxyRef)
			err = e
			if err == nil {
				factory := c.coreClient
				if factory == nil {
					factory = core.NewClient
				}
				client, e := factory(proxy)
				err = e
				if err == nil {
					catalog := core.Catalog{Dir: c.Store.Dir, Client: client}
					co, e := catalog.Install(ctx, action.Version)
					err = e
					prepared = &co
				}
			}
		case CustomCore:
			co, e := core.Inspect(ctx, action.Path)
			err = e
			if err == nil {
				var b []byte
				b, err = os.ReadFile(co.Path)
				if err == nil {
					co.Path = filepath.Join(c.Store.Dir, "cores", co.ID, core.BinaryName())
					err = storage.AtomicWrite(co.Path, b, 0700)
					prepared = &co
				}
			}
		}
	}
	if err == nil {
		var release func()
		release, err = c.acquireCommandScopes(ctx, map[string]uint64{"global/" + scope: 0})
		if err == nil {
			defer release()
			err = c.validateGlobal(cmd, scope)
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			receipt.effect, err = c.executeGlobalAction(ctx, cmd, prepared)
		}
	}
	fallback := domain.StorageFailed
	if _, ok := cmd.Action.(InstallCore); ok {
		fallback = domain.DownloadFailed
	}
	receipt.err = commandError(err, fallback)
	return c.commandReply(receipt.effect, false), receipt.err
}
func (c *Controller) executeGlobalAction(ctx context.Context, cmd GlobalCommand, prepared *domain.Core) (commandEffect, error) {
	effect := commandEffect{}
	switch action := cmd.Action.(type) {
	case InstallCore, CustomCore:
		if prepared == nil {
			return effect, domain.Failure(domain.Validation, "内核尚未完成校验", "coreId", false, nil)
		}
		err := c.addCore(*prepared, &cmd.ExpectedRevision)
		effect.Persisted = err == nil
		return effect, err
	case DeleteCore:
		err := c.deleteCore(action.ID, &cmd.ExpectedRevision)
		_, exists := c.Store.Snapshot().Core(action.ID)
		effect.Persisted = !exists
		return effect, err
	case SetDefaultCore:
		err := c.setDefaultCore(action.ID, &cmd.ExpectedRevision)
		effect.Persisted = err == nil
		return effect, err
	case SaveAppSettings:
		prior := false
		if action.ApplySystemLogin {
			prior = mygo.App.OpenAtLogin()
			if err := mygo.App.SetOpenAtLogin(action.Settings.OpenAtLogin); err != nil {
				return effect, err
			}
		}
		err := c.saveSettings(action.Settings, action.Proxy, &cmd.ExpectedRevision)
		if err != nil && action.ApplySystemLogin {
			if rollback := mygo.App.SetOpenAtLogin(prior); rollback != nil {
				return effect, domain.Failure(domain.StorageFailed, "设置保存失败，系统登录项回退也失败", "openAtLogin", true, errors.Join(err, rollback))
			}
		}
		effect.Persisted = err == nil
		return effect, err
	case RestoreBackup:
		err := c.restoreBackup(action.Data, action.Password, &effect.IDs)
		effect.Persisted = err == nil
		return effect, err
	case ExportBackup:
		if action.Path == "" {
			return effect, domain.Failure(domain.Validation, "缺少备份导出路径", "path", false, nil)
		}
		data, err := c.backup(action.Password, &cmd.ExpectedRevision)
		if err != nil {
			return effect, err
		}
		err = storage.AtomicWrite(action.Path, data, 0600)
		effect.Persisted = err == nil
		return effect, err
	case ImportCA:
		path, err := c.importCA(action.Data)
		effect.Note = path
		effect.Persisted = err == nil
		return effect, err
	case AcknowledgeHideNotice:
		err := c.acknowledgeHideNotice()
		effect.Persisted = err == nil
		return effect, err
	}
	return effect, domain.Failure(domain.Validation, "不支持的全局命令", "kind", false, nil)
}
func (c *Controller) currentGlobal(action Action) GlobalCommand {
	state := c.Store.Snapshot()
	revision := uint64(0)
	switch globalScope(action) {
	case "settings":
		revision = state.Settings.Revision
	case "cores":
		revision = state.CoreRevision
	}
	return GlobalCommand{OperationID: domain.NewID(), ExpectedRevision: revision, Action: action}
}
func (c *Controller) Install(ctx context.Context, version string) error {
	_, err := c.ExecuteGlobal(ctx, c.currentGlobal(InstallCore{Version: version}))
	return err
}
func (c *Controller) CustomCore(ctx context.Context, path string) error {
	_, err := c.ExecuteGlobal(ctx, c.currentGlobal(CustomCore{Path: path}))
	return err
}
func (c *Controller) DeleteCore(id string) error {
	_, err := c.ExecuteGlobal(context.Background(), c.currentGlobal(DeleteCore{ID: id}))
	return err
}
func (c *Controller) SetDefaultCore(id string) error {
	_, err := c.ExecuteGlobal(context.Background(), c.currentGlobal(SetDefaultCore{ID: id}))
	return err
}
func (c *Controller) SaveSettings(settings domain.Settings, proxy *string) error {
	_, err := c.ExecuteGlobal(context.Background(), GlobalCommand{OperationID: domain.NewID(), ExpectedRevision: settings.Revision, Action: SaveAppSettings{Settings: settings, Proxy: proxy}})
	return err
}
func (c *Controller) RestoreBackup(data []byte, password string) error {
	_, err := c.ExecuteGlobal(context.Background(), c.currentGlobal(RestoreBackup{Data: data, Password: password}))
	return err
}
func (c *Controller) ImportCA(data []byte) (string, error) {
	r, err := c.ExecuteGlobal(context.Background(), c.currentGlobal(ImportCA{Data: data}))
	return r.Note, err
}
func (c *Controller) AcknowledgeHideNotice() error {
	_, err := c.ExecuteGlobal(context.Background(), c.currentGlobal(AcknowledgeHideNotice{}))
	return err
}

func (c *Controller) Backup(password string) ([]byte, error) { return c.backup(password, nil) }
