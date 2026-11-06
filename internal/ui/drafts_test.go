package ui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"
	u "github.com/egoist/mygo/ui"
	"sync"
)

func queuedUI(a *App) func(*testing.T) {
	queue := make(chan func(), 8)
	a.dispatch = func(fn func()) { queue <- fn }
	return func(t *testing.T) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for a.Busy {
			select {
			case fn := <-queue:
				fn()
			case <-deadline:
				t.Fatal("UI completion timed out")
			}
		}
	}
}
func savedProfile(t *testing.T, a *App) domain.Profile {
	t.Helper()
	ctx := context.Background()
	if err := a.Controller.Bootstrap(ctx, filepath.Join("..", "..", "resources")); err != nil {
		t.Fatal(err)
	}
	p := domain.NewProfile()
	p.ServerAddr = "127.0.0.1"
	p.TLS = false
	p.VerifyTLS = false
	p.CoreID = a.Controller.Store.Snapshot().Cores[0].ID
	if err := a.Controller.Save(ctx, p, nil, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	a.Refresh()
	p, _ = a.State.Profile(p.ID)
	a.SelectScope(p.ID)
	return p
}

func TestProfileSaveFailureCanRetryWithoutStaleRevision(t *testing.T) {
	a := newTestApp(t)
	drain := queuedUI(a)
	p := savedProfile(t, a)
	a.EditProfile(p.ID)
	drain(t)
	a.ProfileDraft.Name = "已保存的新名称"
	a.ProfileToken = "saved-retry-token"
	a.SaveProfile(true)
	drain(t)
	if !a.ProfileOpen || !strings.Contains(a.FormError, "配置已保存") {
		t.Fatal("apply failure lost editor state", a.FormError)
	}
	saved, _ := a.Controller.Store.Snapshot().Profile(p.ID)
	if a.ProfileDraft.Revision != saved.Revision || a.ProfileDraft.TokenRef != saved.TokenRef || a.HasUnsavedChanges() {
		t.Fatal("editor was not rebased onto its saved version")
	}
	revision := saved.Revision
	a.SaveProfile(true)
	drain(t)
	saved, _ = a.Controller.Store.Snapshot().Profile(p.ID)
	if saved.Revision != revision {
		t.Fatal("retry rewrote an already saved draft")
	}
	a.ProfileDraft.Name = "继续编辑后保存"
	a.SaveProfile(false)
	drain(t)
	saved, _ = a.Controller.Store.Snapshot().Profile(p.ID)
	if a.ProfileOpen || saved.Name != "继续编辑后保存" || saved.Revision != revision+1 {
		t.Fatal("later edit could not save", a.FormError)
	}
}

func TestRetainedDraftAndNewCommandsDoNotOverwriteIt(t *testing.T) {
	a := newTestApp(t)
	a.EditProfile("")
	a.ProfileDraft.Name = "保留的连接"
	id := a.ProfileDraft.ID
	a.EditProfile("")
	if a.ProfileDraft.ID != id || a.ProfileDraft.Name != "保留的连接" {
		t.Fatal("new command overwrote active draft")
	}
	tt := u.NewTester(a.View, 1120, 760)
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if !a.DraftCloseOpen {
		t.Fatal("dirty close had no choice")
	}
	if err := tt.Click("保留草稿并关闭"); err != nil {
		t.Fatal(err)
	}
	if a.ProfileOpen || !a.HasUnsavedChanges() {
		t.Fatal("retained draft was lost")
	}
	a.EditProfile("")
	if a.ProfileDraft.ID != id || a.ProfileDraft.Name != "保留的连接" {
		t.Fatal("retained draft did not resume")
	}
	a.requestEditorClose("profile")
	a.finishEditor("profile", false)
	if a.HasUnsavedChanges() {
		t.Fatal("discarded draft remains dirty")
	}
}

func TestCleanEditorEscapeClosesWithoutDirtyPrompt(t *testing.T) {
	a := newTestApp(t)
	a.EditProfile("")
	tt := u.NewTester(a.View, 1120, 760)
	tt.Key(0, u.KeyEscape)
	if a.ProfileOpen || a.DraftCloseOpen {
		t.Fatal("unchanged editor was treated as modified")
	}
}

func TestTunnelSaveRebaseRetainsTypingAndUsesExistingID(t *testing.T) {
	a := newTestApp(t)
	drain := queuedUI(a)
	p := savedProfile(t, a)
	a.EditTunnel(-1, false)
	a.TunnelDraft.Name = "new-service"
	a.LocalPort = "8080"
	stamp := a.tunnelStamp()
	saved := p
	x := a.TunnelDraft
	x.LocalPort = 8080
	saved.Tunnels = []domain.Tunnel{x}
	if err := a.Controller.Save(context.Background(), saved, nil, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	saved, _ = a.Controller.Store.Snapshot().Profile(p.ID)
	a.TunnelDraft.Name = "typed-during-save"
	a.syncTunnelSave(saved, stamp, nil)
	if a.EditingIndex != 0 || !a.editorDirty("tunnel") || a.TunnelDraft.Name != "typed-during-save" {
		t.Fatal("new typing was lost during save completion")
	}
	a.SaveTunnel(false)
	drain(t)
	saved, _ = a.Controller.Store.Snapshot().Profile(p.ID)
	if len(saved.Tunnels) != 1 || saved.Tunnels[0].ID != x.ID || saved.Tunnels[0].Name != "typed-during-save" {
		t.Fatal("retry appended instead of updating existing item", a.FormError)
	}
}

func TestQuitOnlyPromptsForActualRisk(t *testing.T) {
	cases := []struct {
		n                                  int
		dirty, system, pref, restart, want bool
	}{{0, false, false, true, false, false}, {0, true, false, false, false, true}, {1, false, false, false, false, false}, {1, false, false, false, true, true}, {1, true, true, true, true, false}, {1, false, false, true, false, true}}
	for _, c := range cases {
		if got := QuitNeedsConfirmation(c.n, c.dirty, c.system, c.pref, c.restart); got != c.want {
			t.Fatalf("%+v: %v", c, got)
		}
	}
}

func TestProfileBookkeepingDoesNotDirtySavedDraft(t *testing.T) {
	a := newTestApp(t)
	a.EditProfile("")
	stamp := a.profileStamp()
	p := a.ProfileDraft
	p.Revision = 9
	p.TokenRef = domain.NewID()
	p.MetadataRefs = map[string]string{"token": domain.NewID()}
	a.syncProfileSave(p, stamp)
	if a.editorDirty("profile") {
		t.Fatal("saved references marked as user edits")
	}
	a.ProfileDraft.Name = "new typing"
	if !a.editorDirty("profile") {
		t.Fatal("typing did not mark draft dirty")
	}
}

var _ storage.Secrets = (*storage.MemorySecrets)(nil)

// Block a real credential write so the user can type while Save is in flight.
type gatedSecrets struct {
	storage.Secrets
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *gatedSecrets) Set(k string, v []byte) error {
	s.once.Do(func() { close(s.entered); <-s.release })
	return s.Secrets.Set(k, v)
}
func TestProfileTypingDuringAsyncSaveSurvives(t *testing.T) {
	a := newTestApp(t)
	drain := queuedUI(a)
	p := savedProfile(t, a)
	a.EditProfile(p.ID)
	drain(t)
	gate := &gatedSecrets{Secrets: a.Controller.Secrets, entered: make(chan struct{}), release: make(chan struct{})}
	a.Controller.Secrets = gate
	a.ProfileToken = "new-credential"
	a.ProfileDraft.Name = "submitted-name"
	a.SaveProfile(false)
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("save never reached credential write")
	}
	a.ProfileDraft.Name = "typed-while-saving"
	close(gate.release)
	drain(t)
	saved, _ := a.State.Profile(p.ID)
	if saved.Name != "submitted-name" || !a.ProfileOpen || a.ProfileDraft.Name != "typed-while-saving" || !a.HasUnsavedChanges() || a.ProfileDraft.Revision != saved.Revision {
		t.Fatal("async save lost typing", a.FormError)
	}
	a.SaveProfile(false)
	drain(t)
	saved, _ = a.State.Profile(p.ID)
	if saved.Name != "typed-while-saving" || a.ProfileOpen {
		t.Fatal("rebased draft could not save", a.FormError)
	}
}

func TestRejectedCoreChoiceRemainsAnEdit(t *testing.T) {
	a := newTestApp(t)
	a.State.Cores = []domain.Core{{ID: "old", Version: "0.72.0"}, {ID: "new", Version: "0.72.1"}}
	a.EditProfile("")
	a.ProfileCore = a.coreChoice("new")
	submitted := a.profileEditor
	saved := a.ProfileDraft
	saved.CoreID = "old"
	saved.Revision = 2
	submitted.ProfileCore = a.coreChoice(saved.CoreID)
	a.syncProfileSave(saved, submitted.profileStamp())
	if !a.editorDirty("profile") || a.coreChoiceID(a.ProfileCore) != "new" {
		t.Fatal("rejected core choice incorrectly marked saved")
	}
}

func TestBatchSaveRequiresCurrentPreview(t *testing.T) {
	a := newTestApp(t)
	drain := queuedUI(a)
	_ = savedProfile(t, a)
	a.EditTunnel(-1, false)
	a.Batch = true
	a.LocalPort = "6000-6001"
	a.RemotePort = "7000-7001"
	a.PreviewBatch()
	if a.BatchPreview == "" {
		t.Fatal(a.FormError)
	}
	a.LocalPort = "6000-6019"
	a.RemotePort = "7000-7019"
	a.SaveTunnel(false)
	if a.Busy || !strings.Contains(a.FormError, "重新预览") {
		t.Fatal("stale preview accepted", a.FormError)
	}
	a.PreviewBatch()
	a.SaveTunnel(false)
	drain(t)
	p, _ := a.State.Profile(a.Selected)
	if len(p.Tunnels) != 20 {
		t.Fatal("current preview did not save", a.FormError, len(p.Tunnels))
	}
}

func TestEditorCredentialReadPublishesCompleteSnapshot(t *testing.T) {
	a := newTestApp(t)
	drain := queuedUI(a)
	p := savedProfile(t, a)
	x := domain.NewTunnel()
	x.Type = "stcp"
	x.LocalPort = 8080
	p.Tunnels = []domain.Tunnel{x}
	p.Metadata = map[string]string{"team": "editor-metadata"}
	if err := a.Controller.Save(context.Background(), p, map[string]string{"token": "editor-token", "proxy": "http://user:pass@127.0.0.1:8080", "secret/" + x.ID: "editor-private-key"}, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	a.Refresh()
	a.EditProfile(p.ID)
	if a.ProfileOpen || !a.Busy {
		t.Fatal("editor published before credential read finished")
	}
	drain(t)
	if !a.ProfileOpen || a.ProfileToken != "editor-token" || a.ProfileProxy != "http://user:pass@127.0.0.1:8080" || !strings.Contains(a.ProfileMetadata, "editor-metadata") || a.editorDirty("profile") {
		t.Fatal("credential snapshot incomplete or marked dirty")
	}
	a.finishEditor("profile", false)
	a.EditTunnel(0, false)
	if a.TunnelOpen || !a.Busy {
		t.Fatal("tunnel published before credential read finished")
	}
	drain(t)
	if !a.TunnelOpen || a.TunnelSecret != "editor-private-key" || a.editorDirty("tunnel") {
		t.Fatal("tunnel credential snapshot incomplete")
	}
}
