package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"frp-desktop/internal/storage"
	"maps"
	"os"
	"reflect"
	"sort"

	"frp-desktop/internal/domain"
	"frp-desktop/internal/logging"
	run "frp-desktop/internal/runtime"
)

// CommandMeta is captured with the intent, before work is queued. Operation IDs
// identify one intent for this application session, including failed outcomes.
type CommandMeta struct {
	OperationID      string
	ProfileID        string
	ExpectedRevision uint64
	ExpectedProfiles map[string]uint64 `json:",omitempty"`
}
type Action interface{ commandKind() string }
type Command struct {
	Meta   CommandMeta
	Action Action
}

type SaveProfileDraft struct {
	Profile      domain.Profile
	Values       map[string]string
	RunAfterSave string
}

func (SaveProfileDraft) commandKind() string { return "SaveProfileDraft" }

type CreateProfile SaveProfileDraft

func (CreateProfile) commandKind() string { return "CreateProfile" }

type DeleteProfile struct{}

func (DeleteProfile) commandKind() string { return "DeleteProfile" }

type StartProfile struct{}

func (StartProfile) commandKind() string { return "StartProfile" }

type StopProfile struct{}

func (StopProfile) commandKind() string { return "StopProfile" }

type ApplyProfile struct{ ConnectIfStopped bool }

func (ApplyProfile) commandKind() string { return "ApplyProfile" }

type ImportTOML struct {
	Data []byte
	Name string
}

func (ImportTOML) commandKind() string { return "ImportTOML" }

type SaveRawProfile struct {
	Profile domain.Profile
	Text    string
}

func (SaveRawProfile) commandKind() string { return "SaveRawProfile" }

type ConvertProfileToRaw struct{}

func (ConvertProfileToRaw) commandKind() string { return "ConvertProfileToRaw" }

type DuplicateProfile struct{}

func (DuplicateProfile) commandKind() string { return "DuplicateProfile" }

type RestoreProfile struct{}

func (RestoreProfile) commandKind() string { return "RestoreProfile" }

type SwitchCore struct{ CoreID string }

func (SwitchCore) commandKind() string { return "SwitchCore" }

type SetTunnelEnabled struct{ Items map[string]bool }

func (SetTunnelEnabled) commandKind() string { return "SetTunnelEnabled" }

type ExportProfile struct {
	Path           string
	IncludeSecrets bool
}

func (ExportProfile) commandKind() string { return "ExportProfile" }

type LogSelection struct{ Lines []logging.Line }
type ExportDiagnostics struct {
	Path      string
	Selection *LogSelection
}

func (ExportDiagnostics) commandKind() string { return "ExportDiagnostics" }

type TransformEntries struct {
	IDs         []string
	Destination string
	Remove      bool
}

func (TransformEntries) commandKind() string { return "TransformEntries" }

type ResolveResidual struct {
	Review run.ResidualReview
	Force  bool
}

func (ResolveResidual) commandKind() string { return "ResolveResidual" }

// Receipts retain only identity/outcome metadata, never a command's token,
// configuration text or successful state snapshot. Each reply gets fresh,
// independently owned state alongside the stable original effect.
type commandEffect struct {
	ProfileID string
	IDs       []string
	Note      string
	Persisted bool
}
type CommandResult struct {
	ProfileID string
	IDs       []string
	Note      string
	Persisted bool
	Replayed  bool
	State     domain.State
	Runtime   map[string]domain.Runtime
}
type commandReceipt struct {
	fingerprint [32]byte
	done        chan struct{}
	effect      commandEffect
	err         error
}

func (c *Controller) CurrentCommand(id string, action Action) Command {
	p, _ := c.Store.Snapshot().Profile(id)
	return Command{Meta: CommandMeta{OperationID: domain.NewID(), ProfileID: id, ExpectedRevision: p.Revision}, Action: action}
}
func (c *Controller) Execute(ctx context.Context, cmd Command) (CommandResult, error) {
	if cmd.Action == nil || !domain.ValidID(cmd.Meta.OperationID) || !domain.ValidID(cmd.Meta.ProfileID) {
		return CommandResult{}, domain.Failure(domain.Validation, "命令缺少有效的操作或连接标识", "operationId", false, nil)
	}
	// Accept value and pointer forms, but never invoke a value-receiver method on a nil pointer.
	value := reflect.ValueOf(cmd.Action)
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return CommandResult{}, domain.Failure(domain.Validation, "命令内容不能为空", "kind", false, nil)
		}
		cmd.Action = value.Elem().Interface().(Action)
	}
	var metadata map[string]string
	switch action := cmd.Action.(type) {
	case SaveProfileDraft:
		metadata = action.Profile.Metadata
	case CreateProfile:
		metadata = action.Profile.Metadata
	case SaveRawProfile:
		metadata = action.Profile.Metadata
	}
	// Include the kind so two empty action structs cannot share a receipt. HMAC
	// avoids keeping an offline-guessable digest of short credentials in memory.
	data, err := json.Marshal(struct {
		Kind     string
		Command  Command
		Metadata map[string]string
	}{cmd.Action.commandKind(), cmd, metadata})
	if err != nil {
		return CommandResult{}, domain.Failure(domain.Validation, "命令参数无法编码", "", false, err)
	}
	receipt, replayed, err := c.reserveCommand(ctx, cmd.Meta.OperationID, data)
	if err != nil {
		return CommandResult{}, err
	}
	if replayed {
		return c.commandReply(receipt.effect, true), receipt.err
	}
	defer close(receipt.done)
	scopes := make(map[string]uint64, len(cmd.Meta.ExpectedProfiles)+1)
	scopes[cmd.Meta.ProfileID] = cmd.Meta.ExpectedRevision
	for id, rev := range cmd.Meta.ExpectedProfiles {
		if !domain.ValidID(id) || id == cmd.Meta.ProfileID && rev != cmd.Meta.ExpectedRevision {
			receipt.err = domain.Failure(domain.Validation, "命令的连接版本条件不一致", "revision", false, nil)
			return c.commandReply(receipt.effect, false), receipt.err
		}
		scopes[id] = rev
	}
	release, err := c.acquireCommandScopes(ctx, scopes)
	if err != nil {
		receipt.err = commandError(err, domain.Canceled)
		return c.commandReply(receipt.effect, false), receipt.err
	}
	defer release()
	if err = ctx.Err(); err == nil {
		err = c.validateCommand(cmd, scopes)
	}
	if err == nil {
		receipt.effect, err = c.executeAction(ctx, cmd)
	}
	receipt.err = commandError(err, commandFallback(cmd.Action))
	return c.commandReply(receipt.effect, false), receipt.err
}
func commandFallback(action Action) domain.ErrorCode {
	switch action.(type) {
	case StartProfile:
		return domain.StartFailed
	case StopProfile, ApplyProfile, SwitchCore, SetTunnelEnabled:
		return domain.ApplyFailed
	case ImportTOML:
		return domain.Validation
	default:
		return domain.StorageFailed
	}
}
func (c *Controller) commandReply(effect commandEffect, replayed bool) CommandResult {
	return CommandResult{ProfileID: effect.ProfileID, IDs: append([]string(nil), effect.IDs...), Note: effect.Note, Persisted: effect.Persisted, Replayed: replayed, State: c.Store.Snapshot(), Runtime: c.Runtime.Snapshot()}
}
func (c *Controller) acquireCommandScopes(ctx context.Context, scopes map[string]uint64) (func(), error) {
	ids := make([]string, 0, len(scopes))
	for id := range scopes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	held := make([]chan struct{}, 0, len(ids))
	release := func() {
		for i := len(held) - 1; i >= 0; i-- {
			<-held[i]
		}
	}
	for _, id := range ids {
		c.commandMu.Lock()
		gate := c.commandScopes[id]
		if gate == nil {
			gate = make(chan struct{}, 1)
			c.commandScopes[id] = gate
		}
		c.commandMu.Unlock()
		select {
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		case gate <- struct{}{}:
			held = append(held, gate)
		}
	}
	return release, nil
}
func (c *Controller) validateCommand(cmd Command, scopes map[string]uint64) error {
	state := c.Store.Snapshot()
	for id, expected := range scopes {
		p, exists := state.Profile(id)
		if id == cmd.Meta.ProfileID {
			switch cmd.Action.(type) {
			case CreateProfile, ImportTOML:
				if exists || expected != 0 {
					return domain.Failure(domain.Conflict, "新建连接标识已经存在或版本条件无效", "revision", false, nil)
				}
				continue
			case RestoreProfile:
				if exists {
					return domain.Failure(domain.Conflict, "连接已经恢复或标识被占用", "revision", false, nil)
				}
				found := false
				for _, deleted := range state.Trash {
					if deleted.Profile.ID == id {
						found = true
						if deleted.Profile.Revision != expected {
							return storageConflict()
						}
						break
					}
				}
				if !found {
					return domain.Failure(domain.NotFound, "回收区连接不存在", "profileId", false, os.ErrNotExist)
				}
				continue
			}
		}
		if !exists {
			return domain.Failure(domain.NotFound, "连接不存在", "profileId", false, os.ErrNotExist)
		}
		if p.Revision != expected {
			return storageConflict()
		}
	}
	switch action := cmd.Action.(type) {
	case SaveProfileDraft:
		return validateSaveTarget(action, cmd.Meta)
	case CreateProfile:
		return validateSaveTarget(SaveProfileDraft(action), cmd.Meta)
	case TransformEntries:
		selected := map[string]bool{}
		for _, id := range action.IDs {
			selected[id] = true
		}
		found := map[string]bool{}
		for _, p := range state.Profiles {
			requireScope := false
			for _, x := range p.Tunnels {
				if selected[x.ID] {
					found[x.ID] = true
					requireScope = true
				}
			}
			for _, x := range p.Visitors {
				if selected[x.ID] {
					found[x.ID] = true
					requireScope = true
				}
			}
			if requireScope {
				if _, ok := scopes[p.ID]; !ok {
					return domain.Failure(domain.Validation, "命令缺少受影响连接的版本条件", "expectedProfiles", false, nil)
				}
			}
		}
		if len(found) != len(selected) || len(found) == 0 {
			return domain.Failure(domain.NotFound, "所选条目已经不存在", "items", false, os.ErrNotExist)
		}
		if !action.Remove {
			if _, ok := scopes[action.Destination]; !ok {
				return domain.Failure(domain.Validation, "命令缺少目标连接的版本条件", "expectedProfiles", false, nil)
			}
		}
	case ResolveResidual:
		if action.Review.ProfileID != cmd.Meta.ProfileID {
			return domain.Failure(domain.Validation, "残留审查记录不属于此连接", "profileId", false, nil)
		}
	case SaveRawProfile:
		if action.Profile.ID != cmd.Meta.ProfileID || action.Profile.Revision != cmd.Meta.ExpectedRevision {
			return storageConflict()
		}
	}
	return nil
}
func storageConflict() error {
	return domain.Failure(domain.Conflict, "配置已改变，未执行旧版本命令；请刷新后重试", "revision", false, storage.ErrConflict)
}
func validateSaveTarget(action SaveProfileDraft, meta CommandMeta) error {
	if action.Profile.ID != meta.ProfileID || action.Profile.Revision != meta.ExpectedRevision {
		return storageConflict()
	}
	if action.RunAfterSave != "" && action.RunAfterSave != "apply" && action.RunAfterSave != "connect" {
		return domain.Failure(domain.Validation, "无效的保存后操作", "runAfterSave", false, nil)
	}
	return nil
}
func cloneCommandProfile(p domain.Profile) domain.Profile {
	data, _ := json.Marshal(p)
	var copy domain.Profile
	_ = json.Unmarshal(data, &copy)
	copy.Metadata = maps.Clone(p.Metadata)
	return copy
}
func (c *Controller) executeAction(ctx context.Context, cmd Command) (commandEffect, error) {
	effect := commandEffect{ProfileID: cmd.Meta.ProfileID}
	switch action := cmd.Action.(type) {
	case CreateProfile:
		return c.saveCommand(ctx, SaveProfileDraft(action))
	case SaveProfileDraft:
		return c.saveCommand(ctx, action)
	case StartProfile:
		effect.Persisted = true
		return effect, c.startProfile(ctx, cmd.Meta.ProfileID)
	case StopProfile:
		return effect, c.Runtime.Stop(cmd.Meta.ProfileID)
	case ApplyProfile:
		effect.Persisted = true
		if action.ConnectIfStopped && !c.Runtime.Snapshot()[cmd.Meta.ProfileID].Running() {
			return effect, c.startProfile(ctx, cmd.Meta.ProfileID)
		}
		return effect, c.applyProfile(ctx, cmd.Meta.ProfileID)
	case DeleteProfile:
		err := c.deleteProfile(cmd.Meta.ProfileID)
		effect.Persisted = err == nil
		return effect, err
	case ImportTOML:
		id, note, err := c.importTOML(ctx, action.Data, action.Name, cmd.Meta.ProfileID)
		effect.ProfileID, effect.Note, effect.Persisted = id, note, err == nil
		return effect, err
	case SaveRawProfile:
		err := c.saveRaw(ctx, cloneCommandProfile(action.Profile), action.Text)
		effect.Persisted = err == nil
		return effect, err
	case ConvertProfileToRaw:
		err := c.convertToRaw(ctx, cmd.Meta.ProfileID)
		effect.Persisted = err == nil
		return effect, err
	case DuplicateProfile:
		id := domain.NewID()
		err := c.duplicate(ctx, cmd.Meta.ProfileID, id)
		effect.ProfileID = id
		effect.Persisted = err == nil
		return effect, err
	case RestoreProfile:
		err := c.Store.Restore(cmd.Meta.ProfileID)
		effect.Persisted = err == nil
		return effect, err
	case SetTunnelEnabled:
		p, _ := c.Store.Snapshot().Profile(cmd.Meta.ProfileID)
		found := map[string]bool{}
		for i := range p.Tunnels {
			if enabled, ok := action.Items[p.Tunnels[i].ID]; ok {
				p.Tunnels[i].Enabled = enabled
				found[p.Tunnels[i].ID] = true
			}
		}
		for i := range p.Visitors {
			if enabled, ok := action.Items[p.Visitors[i].ID]; ok {
				p.Visitors[i].Enabled = enabled
				found[p.Visitors[i].ID] = true
			}
		}
		if len(found) != len(action.Items) || len(found) == 0 {
			return effect, domain.Failure(domain.Validation, "所选条目已不存在或不属于此连接", "items", false, nil)
		}
		return c.saveCommand(ctx, SaveProfileDraft{Profile: p, RunAfterSave: "apply"})
	case ExportProfile:
		if action.Path == "" {
			return effect, domain.Failure(domain.Validation, "缺少导出路径", "path", false, nil)
		}
		data, err := c.Export(cmd.Meta.ProfileID, action.IncludeSecrets)
		if err != nil {
			return effect, err
		}
		return effect, storage.AtomicWrite(action.Path, data, 0600)
	case ExportDiagnostics:
		if action.Path == "" {
			return effect, domain.Failure(domain.Validation, "缺少导出路径", "path", false, nil)
		}
		var data []byte
		var err error
		if action.Selection == nil {
			data, err = c.Diagnostics(cmd.Meta.ProfileID)
		} else {
			data, err = c.diagnosticsSelection(cmd.Meta.ProfileID, action.Selection.Lines)
		}
		if err != nil {
			return effect, err
		}
		return effect, storage.AtomicWrite(action.Path, data, 0600)
	case TransformEntries:
		ids, err := c.transformEntries(ctx, action.IDs, action.Destination, action.Remove)
		if err != nil {
			return effect, err
		}
		sort.Strings(ids)
		effect.IDs = ids
		effect.Persisted = true
		var errs []error
		for _, id := range ids {
			if err = c.applyProfile(ctx, id); err != nil {
				errs = append(errs, fmt.Errorf("连接 %s：%w", id, err))
			}
		}
		return effect, errors.Join(errs...)
	case ResolveResidual:
		return effect, c.Runtime.ResolveResidual(ctx, action.Review, action.Force)
	case SwitchCore:
		err := c.switchCore(ctx, cmd.Meta.ProfileID, action.CoreID)
		current, ok := c.Store.Snapshot().Profile(cmd.Meta.ProfileID)
		effect.Persisted = ok && current.Revision != cmd.Meta.ExpectedRevision
		return effect, err
	default:
		return effect, domain.Failure(domain.Validation, "不支持的命令类型", "kind", false, nil)
	}
}
func (c *Controller) saveCommand(ctx context.Context, action SaveProfileDraft) (commandEffect, error) {
	p := cloneCommandProfile(action.Profile)
	effect := commandEffect{ProfileID: p.ID}
	if err := c.save(ctx, p, action.Values); err != nil {
		return effect, err
	}
	effect.Persisted = true
	switch action.RunAfterSave {
	case "apply":
		return effect, c.applyProfile(ctx, p.ID)
	case "connect":
		if c.Runtime.Snapshot()[p.ID].Running() {
			return effect, c.applyProfile(ctx, p.ID)
		}
		return effect, c.startProfile(ctx, p.ID)
	}
	return effect, nil
}

// Compatibility entry points also go through the command contract. UI actions
// that already hold a snapshot use Execute directly instead of reading a newer
// revision at the point a worker happens to start.
func (c *Controller) Save(ctx context.Context, p domain.Profile, values map[string]string, operation string) error {
	var action Action = SaveProfileDraft{Profile: p, Values: values}
	if p.Revision == 0 {
		action = CreateProfile(SaveProfileDraft{Profile: p, Values: values})
	}
	_, err := c.Execute(ctx, Command{Meta: CommandMeta{OperationID: operation, ProfileID: p.ID, ExpectedRevision: p.Revision}, Action: action})
	return err
}
func (c *Controller) Start(ctx context.Context, id string) error {
	_, err := c.Execute(ctx, c.CurrentCommand(id, StartProfile{}))
	return err
}
func (c *Controller) Apply(ctx context.Context, id string) error {
	_, err := c.Execute(ctx, c.CurrentCommand(id, ApplyProfile{}))
	return err
}
func (c *Controller) Delete(id string) error {
	_, err := c.Execute(context.Background(), c.CurrentCommand(id, DeleteProfile{}))
	return err
}
func (c *Controller) Import(ctx context.Context, data []byte, name string) (string, string, error) {
	r, err := c.Execute(ctx, Command{Meta: CommandMeta{OperationID: domain.NewID(), ProfileID: domain.NewID()}, Action: ImportTOML{Data: data, Name: name}})
	return r.ProfileID, r.Note, err
}
func (c *Controller) SaveRaw(ctx context.Context, p domain.Profile, text string) error {
	_, err := c.Execute(ctx, Command{Meta: CommandMeta{OperationID: domain.NewID(), ProfileID: p.ID, ExpectedRevision: p.Revision}, Action: SaveRawProfile{Profile: p, Text: text}})
	return err
}
func (c *Controller) ConvertToRaw(ctx context.Context, id string) error {
	_, err := c.Execute(ctx, c.CurrentCommand(id, ConvertProfileToRaw{}))
	return err
}
func (c *Controller) Duplicate(ctx context.Context, id string) error {
	_, err := c.Execute(ctx, c.CurrentCommand(id, DuplicateProfile{}))
	return err
}
func (c *Controller) SwitchCore(ctx context.Context, id, coreID string) error {
	_, err := c.Execute(ctx, c.CurrentCommand(id, SwitchCore{CoreID: coreID}))
	return err
}

func (c *Controller) TransformEntries(ctx context.Context, ids []string, destination string, remove bool) ([]string, error) {
	cmd, err := EntryCommand(c.Store.Snapshot(), ids, destination, remove)
	if err != nil {
		return nil, err
	}
	result, err := c.Execute(ctx, cmd)
	return result.IDs, err
}

// EntryCommand captures every source/destination revision before the worker is queued.
func EntryCommand(state domain.State, ids []string, destination string, remove bool) (Command, error) {
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	expected := map[string]uint64{}
	for _, p := range state.Profiles {
		needed := !remove && p.ID == destination
		for _, x := range p.Tunnels {
			needed = needed || selected[x.ID]
		}
		for _, x := range p.Visitors {
			needed = needed || selected[x.ID]
		}
		if needed {
			expected[p.ID] = p.Revision
		}
	}
	if len(expected) == 0 {
		return Command{}, domain.Failure(domain.NotFound, "没有可操作的连接或条目", "items", false, nil)
	}
	profiles := make([]string, 0, len(expected))
	for id := range expected {
		profiles = append(profiles, id)
	}
	sort.Strings(profiles)
	return Command{Meta: CommandMeta{OperationID: domain.NewID(), ProfileID: profiles[0], ExpectedRevision: expected[profiles[0]], ExpectedProfiles: expected}, Action: TransformEntries{IDs: append([]string(nil), ids...), Destination: destination, Remove: remove}}, nil
}
