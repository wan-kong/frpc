package ui

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	"frp-desktop/internal/domain"
	u "github.com/egoist/mygo/ui"
)

type profileEditor struct {
	ProfileDraft                                                          domain.Profile
	ProfileToken, ProfileProxy, ProfileMetadata, ProfileCore, ProfilePort string
	profileKey                                                            string
	profileBaseline                                                       [32]byte
}
type tunnelEditor struct {
	TunnelProfile                                                         domain.Profile
	TunnelDraft                                                           domain.Tunnel
	VisitorDraft                                                          domain.Visitor
	EditingVisitor                                                        bool
	EditingIndex                                                          int
	TunnelSecret, HTTPPassword, LocalPort, RemotePort, Domains, Locations string
	Batch                                                                 bool
	BatchPreview                                                          string
	batchPreviewStamp                                                     [32]byte
	tunnelKey                                                             string
	tunnelBaseline                                                        [32]byte
	batchSavedIDs                                                         []string
}
type rawEditor struct {
	RawProfile  domain.Profile
	RawText     string
	rawBaseline [32]byte
}

func digest(value any) [32]byte { b, _ := json.Marshal(value); return sha256.Sum256(b) }
func (a *profileEditor) profileStamp() [32]byte {
	p := a.ProfileDraft
	p.Revision = 0
	p.TokenRef = ""
	p.ProxyURLRef = ""
	p.MetadataRefs = nil
	p.Metadata = nil
	p.CoreID = ""
	p.CoreVersion = ""
	p.ServerPort = 0
	p.Tunnels = nil
	p.Visitors = nil
	return digest(struct {
		P                                  domain.Profile
		Token, Proxy, Metadata, Core, Port string
	}{p, a.ProfileToken, a.ProfileProxy, a.ProfileMetadata, a.ProfileCore, a.ProfilePort})
}
func (a *App) tunnelStamp() [32]byte {
	t, v := a.TunnelDraft, a.VisitorDraft
	t.SecretRef = ""
	t.HTTPPasswordRef = ""
	t.LocalPort = 0
	t.RemotePort = 0
	t.Domains = nil
	t.Locations = nil
	v.SecretRef = ""
	v.BindPort = 0
	return digest(struct {
		T                                               domain.Tunnel
		V                                               domain.Visitor
		Visitor                                         bool
		Secret, HTTP, Local, Remote, Domains, Locations string
		Batch                                           bool
	}{t, v, a.EditingVisitor, a.TunnelSecret, a.HTTPPassword, a.LocalPort, a.RemotePort, a.Domains, a.Locations, a.Batch})
}
func (a *App) HasModal() bool {
	return a.ProfileOpen || a.TunnelOpen || a.RawOpen || a.BackupOpen || a.MoveOpen || a.PortsOpen || a.CompactInspector || a.UpdateOpen || a.DraftCloseOpen || a.ResidualOpen
}
func (a *App) editorDirty(kind string) bool {
	switch kind {
	case "profile":
		return a.profileStamp() != a.profileBaseline
	case "tunnel":
		return a.tunnelStamp() != a.tunnelBaseline
	case "raw":
		return a.RawProfile.Mode == "raw" && digest(a.RawText) != a.rawBaseline
	}
	return false
}
func (a *App) HasUnsavedChanges() bool {
	return len(a.pendingToggles) > 0 || a.ProfileOpen && a.editorDirty("profile") || a.TunnelOpen && a.editorDirty("tunnel") || a.RawOpen && a.editorDirty("raw") || len(a.profileDrafts)+len(a.tunnelDrafts)+len(a.rawDrafts) > 0
}
func (a *App) requestEditorClose(kind string) {
	if a.Busy {
		return
	}
	if a.editorDirty(kind) {
		a.DraftCloseKind = kind
		a.DraftCloseOpen = true
		return
	}
	a.finishEditor(kind, false)
}
func (a *App) finishEditor(kind string, keep bool) {
	switch kind {
	case "profile":
		if keep {
			if a.profileDrafts == nil {
				a.profileDrafts = map[string]profileEditor{}
			}
			a.profileDrafts[a.profileKey] = a.profileEditor
		} else {
			delete(a.profileDrafts, a.profileKey)
		}
		a.ProfileOpen = false
		a.profileEditor = profileEditor{}
	case "tunnel":
		if keep {
			if a.tunnelDrafts == nil {
				a.tunnelDrafts = map[string]tunnelEditor{}
			}
			a.tunnelDrafts[a.tunnelKey] = a.tunnelEditor
		} else {
			delete(a.tunnelDrafts, a.tunnelKey)
		}
		a.TunnelOpen = false
		a.tunnelEditor = tunnelEditor{}
	case "raw":
		if keep {
			if a.rawDrafts == nil {
				a.rawDrafts = map[string]rawEditor{}
			}
			a.rawDrafts[a.RawProfile.ID] = a.rawEditor
		} else {
			delete(a.rawDrafts, a.RawProfile.ID)
		}
		a.RawOpen = false
		a.rawEditor = rawEditor{}
	}
	a.DraftCloseOpen = false
	a.FormError = ""
	a.FormField = ""
}
func (a *App) draftCloseModal(c *u.Context) {
	u.Modal(c, &a.DraftCloseOpen, func() {
		u.Column(c).Width(440).Gap(12).Children(func() {
			u.Text(c, "尚有未保存的修改").FontSize(18).Bold()
			u.Text(c, "保留的草稿只在本次应用会话中保存，可从窗口底部继续编辑。").Wrap()
			u.Row(c).Gap(8).Children(func() {
				u.Button(c, "继续编辑").OnClick(func() { a.DraftCloseOpen = false })
				u.Button(c, "放弃修改").OnClick(func() { a.finishEditor(a.DraftCloseKind, false) })
				u.PrimaryButton(c, "保留草稿并关闭").OnClick(func() { a.finishEditor(a.DraftCloseKind, true) })
			})
		})
	})
}
func (a *App) draftMenu(c *u.Context) {
	n := len(a.profileDrafts) + len(a.tunnelDrafts) + len(a.rawDrafts)
	if n == 0 {
		return
	}
	u.MenuButton(c, fmt.Sprintf("继续草稿（%d）", n), func(m *u.Menu) {
		var keys []string
		for k := range a.profileDrafts {
			keys = append(keys, "p/"+k)
		}
		for k := range a.tunnelDrafts {
			keys = append(keys, "t/"+k)
		}
		for k := range a.rawDrafts {
			keys = append(keys, "r/"+k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			label := ""
			switch key[:2] {
			case "p/":
				label = "连接 · " + a.profileDrafts[key[2:]].ProfileDraft.Name
			case "t/":
				s := a.tunnelDrafts[key[2:]]
				name := s.TunnelDraft.Name
				if s.EditingVisitor {
					name = s.VisitorDraft.Name
				}
				label = "隧道/访问者 · " + s.TunnelProfile.Name + " / " + name
			case "r/":
				label = "原文 · " + a.rawDrafts[key[2:]].RawProfile.Name
			}
			if m.Item(label).Disabled(a.HasModal() || a.Busy).Chosen() {
				switch key[:2] {
				case "p/":
					a.profileEditor = a.profileDrafts[key[2:]]
					delete(a.profileDrafts, key[2:])
					a.ProfileOpen = true
				case "t/":
					a.tunnelEditor = a.tunnelDrafts[key[2:]]
					delete(a.tunnelDrafts, key[2:])
					a.TunnelOpen = true
				case "r/":
					a.rawEditor = a.rawDrafts[key[2:]]
					delete(a.rawDrafts, key[2:])
					a.RawOpen = true
				}
				a.FormError = ""
			}
		}
	}).Margin(4, 12)
}
func (a *App) syncProfileSave(saved domain.Profile, stamp [32]byte) {
	delete(a.profileDrafts, a.profileKey)
	a.profileKey = saved.ID
	a.ProfileDraft.Revision = saved.Revision
	a.ProfileDraft.TokenRef = saved.TokenRef
	a.ProfileDraft.ProxyURLRef = saved.ProxyURLRef
	a.ProfileDraft.MetadataRefs = saved.MetadataRefs
	a.ProfileDraft.CoreID = saved.CoreID
	a.ProfileDraft.CoreVersion = saved.CoreVersion
	a.profileBaseline = stamp
}
func (a *App) syncTunnelSave(saved domain.Profile, stamp [32]byte, batchIDs []string) {
	a.TunnelProfile = saved
	a.tunnelBaseline = stamp
	if batchIDs != nil {
		a.batchSavedIDs = batchIDs
	}
	if a.EditingVisitor {
		for i, v := range saved.Visitors {
			if v.ID == a.VisitorDraft.ID {
				a.EditingIndex = i
				a.VisitorDraft.SecretRef = v.SecretRef
				break
			}
		}
	} else {
		for i, t := range saved.Tunnels {
			if t.ID == a.TunnelDraft.ID {
				a.EditingIndex = i
				a.TunnelDraft.SecretRef = t.SecretRef
				a.TunnelDraft.HTTPPasswordRef = t.HTTPPasswordRef
				break
			}
		}
	}
}
