package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/krilinxito/studyhomie/internal/cards"
	"github.com/krilinxito/studyhomie/internal/i18n"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// Ajustes → Datos: what the vault holds by category, deleting into a trash that can be undone,
// and cleaning up what deleted notes left behind.

// cardStateFile is the name under which a trash item keeps review states taken from cards.json.
const cardStateFile = "cards-state.json"

var (
	errItemInUse   = errors.New(i18n.N("eso lo está usando una sesión en curso; termínala o cancélala primero"))
	errDataBusy    = errors.New(i18n.N("hay algo en curso; espera a que termine para vaciar esto"))
	errRestoreBusy = errors.New(i18n.N("ya existe otra nota en ese lugar; bórrala o renómbrala antes de restaurar"))
)

// purgeTrash drops what has been in the trash longer than vault.TrashKeep.
func purgeTrash(v *vault.Vault) { v.TrashPurgeOlder(vault.TrashKeep, time.Now()) }

// working reports whether a flow is running right now (a chat waiting for a message is not).
func (s *Server) working() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.runs {
		if !r.isDone() && r.waiting() != "chat" {
			return true
		}
	}
	return false
}

// inUse reports whether a running flow works on this item.
func (s *Server) inUse(cat, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.runs {
		if r.isDone() {
			continue
		}
		switch cat {
		case vault.CatGuides, vault.CatRoadmaps, vault.CatDecks, vault.CatHandoffs, vault.CatSources, vault.CatInteractive, vault.CatMaterial:
			if r.Slug == id {
				return true
			}
		case vault.CatSessions:
			if strconv.Itoa(r.ID) == id {
				return true
			}
		}
	}
	return false
}

func (s *Server) dataOverview(w http.ResponseWriter, r *http.Request) {
	v := s.vault()
	n, b := v.TrashSize()
	var ob int64
	orphans := s.orphans()
	for _, o := range orphans {
		ob += o.Bytes
	}
	writeJSON(w, 200, map[string]any{
		"categories": v.Inventory(),
		"trash":      map[string]any{"count": n, "bytes": b, "keepDays": int(vault.TrashKeep.Hours() / 24)},
		"orphans":    map[string]any{"count": len(orphans), "bytes": ob},
	})
}

func (s *Server) dataItems(w http.ResponseWriter, r *http.Request) {
	cat := r.PathValue("cat")
	if !vault.IsCategory(cat) {
		fail(w, 404, "categoría desconocida")
		return
	}
	items, err := s.vault().Items(cat)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if items == nil {
		items = []vault.DataItem{}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

// dataDelete moves one item to the trash. For a guide, ?with=deck,sessions also takes its deck
// (with its review progress) and the chat sessions about it.
func (s *Server) dataDelete(w http.ResponseWriter, r *http.Request) {
	cat, id := r.PathValue("cat"), r.PathValue("id")
	if !vault.IsCategory(cat) {
		fail(w, 404, "categoría desconocida")
		return
	}
	if s.inUse(cat, id) {
		fail(w, http.StatusConflict, errItemInUse.Error())
		return
	}
	var with []string
	if q := r.URL.Query().Get("with"); q != "" {
		with = strings.Split(q, ",")
	}
	item, err := s.removeItem(cat, id, with)
	if errors.Is(err, os.ErrNotExist) {
		fail(w, 404, "no existe eso")
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "trash": item})
}

// removeItem moves an item to the trash and keeps the server's own state in step with it.
func (s *Server) removeItem(cat, id string, with []string) (vault.TrashItem, error) {
	v := s.vault()
	switch cat {
	case vault.CatGuides:
		var paths []string
		data := map[string][]byte{}
		if slices.Contains(with, "deck") {
			paths = append(paths, vault.FlashcardsDir+"/"+id+".md")
			if st, err := cards.TakeDeckStates(v.Root, id); err == nil && len(st) > 0 {
				data[cardStateFile], _ = json.Marshal(st)
			}
		}
		var sessions []int
		if slices.Contains(with, "sessions") {
			sessions = s.sessionsAbout(id)
			for _, sid := range sessions {
				paths = append(paths, ".studyhomie/sessions/"+strconv.Itoa(sid)+".json")
			}
		}
		item, err := v.DeleteGuide(id, paths, data)
		if err != nil {
			if st := data[cardStateFile]; st != nil {
				s.putStates(st)
			}
			return item, err
		}
		s.forgetSessions(sessions)
		return item, nil
	case vault.CatDecks:
		data := map[string][]byte{}
		if st, err := cards.TakeDeckStates(v.Root, id); err == nil && len(st) > 0 {
			data[cardStateFile], _ = json.Marshal(st)
		}
		item, err := v.RemoveItem(cat, id, data)
		if err != nil && data[cardStateFile] != nil {
			s.putStates(data[cardStateFile])
		}
		return item, err
	case vault.CatSessions:
		sid, err := strconv.Atoi(id)
		if err != nil {
			return vault.TrashItem{}, os.ErrNotExist
		}
		item, err := v.RemoveItem(cat, id, nil)
		if err == nil {
			s.forgetSessions([]int{sid})
		}
		return item, err
	}
	return v.RemoveItem(cat, id, nil)
}

func (s *Server) putStates(raw []byte) {
	var st map[string]cards.ReviewState
	if json.Unmarshal(raw, &st) == nil {
		_ = cards.PutStates(s.vault().Root, st)
	}
}

// sessionsAbout lists the finished sessions that worked on a guide.
func (s *Server) sessionsAbout(slug string) []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []int
	for _, r := range s.runs {
		if r.isDone() && r.Slug == slug {
			out = append(out, r.ID)
		}
	}
	slices.Sort(out)
	return out
}

// forgetSessions drops sessions whose files went to the trash from the history.
func (s *Server) forgetSessions(ids []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if r := s.runs[id]; r != nil && s.active == r {
			s.active = nil
		}
		delete(s.runs, id)
	}
}

// reloadSessions brings restored session files back into the history.
func (s *Server) reloadSessions(files []string) {
	if s.store == nil {
		return
	}
	var ids []int
	for _, f := range files {
		name, ok := strings.CutPrefix(f, ".studyhomie/sessions/")
		if !ok {
			continue
		}
		if id, err := strconv.Atoi(strings.TrimSuffix(name, ".json")); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sf := range s.store.load() {
		if !slices.Contains(ids, sf.ID) {
			continue
		}
		if _, ok := s.runs[sf.ID]; ok {
			continue
		}
		run := restore(sf)
		run.store = s.store
		s.runs[sf.ID] = run
		if sf.ID > s.seq {
			s.seq = sf.ID
		}
	}
}

// dataEmpty moves every item of a category to the trash (one trash item each); ?with as in dataDelete.
func (s *Server) dataEmpty(w http.ResponseWriter, r *http.Request) {
	cat := r.PathValue("cat")
	if !vault.IsCategory(cat) {
		fail(w, 404, "categoría desconocida")
		return
	}
	if s.working() {
		fail(w, http.StatusConflict, errDataBusy.Error())
		return
	}
	var with []string
	if q := r.URL.Query().Get("with"); q != "" {
		with = strings.Split(q, ",")
	}
	items, err := s.vault().Items(cat)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	n := 0
	for _, it := range items {
		if s.inUse(cat, it.ID) {
			continue
		}
		if _, err := s.removeItem(cat, it.ID, with); err == nil {
			n++
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "removed": n})
}

// orphan is a leftover the cleanup removes; cardstate stands for review states without a card.
func (s *Server) orphans() []vault.Orphan {
	v := s.vault()
	var out []vault.Orphan
	for _, o := range v.Orphans(time.Now()) {
		if s.inUse(o.Cat, o.ID) || (o.Cat == vault.CatUploads && s.working()) {
			continue
		}
		out = append(out, o)
	}
	if n := cards.OrphanStates(v.Root); n > 0 {
		out = append(out, vault.Orphan{Cat: "cardstate", ID: "cards", Title: strconv.Itoa(n)})
	}
	return out
}

func (s *Server) dataOrphans(w http.ResponseWriter, r *http.Request) {
	list := s.orphans()
	if list == nil {
		list = []vault.Orphan{}
	}
	writeJSON(w, 200, map[string]any{"orphans": list})
}

// cleanOrphans moves every leftover to the trash as one item.
func (s *Server) cleanOrphans(w http.ResponseWriter, r *http.Request) {
	v := s.vault()
	list := s.orphans()
	var files []string
	data := map[string][]byte{}
	for _, o := range list {
		if o.Cat == "cardstate" {
			if st, err := cards.DropOrphanStates(v.Root); err == nil && len(st) > 0 {
				data[cardStateFile], _ = json.Marshal(st)
			}
			continue
		}
		files = append(files, o.Rel)
	}
	item, err := v.Trash(vault.TrashItem{Kind: "orphans", Title: "orphans", Files: files}, data)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, 200, map[string]any{"ok": true, "removed": 0})
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "removed": len(list), "trash": item})
}

func (s *Server) trashList(w http.ResponseWriter, r *http.Request) {
	items, err := s.vault().TrashList()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if items == nil {
		items = []vault.TrashItem{}
	}
	writeJSON(w, 200, map[string]any{"items": items, "keepDays": int(vault.TrashKeep.Hours() / 24)})
}

func (s *Server) trashRestore(w http.ResponseWriter, r *http.Request) {
	item, extra, err := s.vault().Restore(r.PathValue("id"))
	switch {
	case errors.Is(err, vault.ErrRestoreConflict):
		fail(w, http.StatusConflict, errRestoreBusy.Error())
		return
	case errors.Is(err, os.ErrNotExist):
		fail(w, 404, "eso ya no está en la papelera")
		return
	case err != nil:
		fail(w, 500, err.Error())
		return
	}
	if st := extra[cardStateFile]; st != nil {
		s.putStates(st)
	}
	s.reloadSessions(item.Files)
	writeJSON(w, 200, map[string]any{"ok": true, "item": item})
}

func (s *Server) trashPurge(w http.ResponseWriter, r *http.Request) {
	if err := s.vault().TrashPurge(r.PathValue("id")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fail(w, 404, "eso ya no está en la papelera")
			return
		}
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) trashEmpty(w http.ResponseWriter, r *http.Request) {
	if err := s.vault().TrashEmpty(); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
