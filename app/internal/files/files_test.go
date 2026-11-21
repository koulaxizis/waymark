package files

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/koulaxizis/waymark/app/internal/proto"
)

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func newStore(t *testing.T) *Store {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestClean(t *testing.T) {
	ok := map[string]string{
		"a.pptx":                 "a.pptx",
		"Ομιλία/Διαφάνειες.pptx": "Ομιλία/Διαφάνειες.pptx",
		`dir\sub\x.pdf`:          "dir/sub/x.pdf",
		"./a//b.pdf":             "a/b.pdf",
	}
	for in, want := range ok {
		got, err := Clean(in)
		if err != nil || got != want {
			t.Errorf("Clean(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "/etc/passwd", "../x", "a/../../x", "C:/x", "a/CON.txt", "a/b?.pdf", ".waymark/partial/x", "x. ", "trail."} {
		if _, err := Clean(bad); err == nil {
			t.Errorf("Clean(%q) accepted", bad)
		}
	}
}

func TestUploadResumeAndUnique(t *testing.T) {
	s := newStore(t)
	body := make([]byte, 150000)
	for i := range body {
		body[i] = byte(i * 7)
	}
	offer := proto.OfferArgs{Path: "Αίθουσα Α/talk.pptx", Size: int64(len(body)), SHA256: sum(body)}
	r, err := s.Offer(offer)
	if err != nil || r.Offset != 0 || r.Path != "Αίθουσα Α/talk.pptx" {
		t.Fatalf("offer %+v %v", r, err)
	}
	if _, err := s.Chunk(proto.ChunkArgs{ID: r.ID, Offset: 0, Data: body[:65536]}); err != nil {
		t.Fatal(err)
	}
	// Drop: offer again, it resumes.
	r2, err := s.Offer(offer)
	if err != nil || r2.ID != r.ID || r2.Offset != 65536 {
		t.Fatalf("resume %+v %v", r2, err)
	}
	if _, err := s.Chunk(proto.ChunkArgs{ID: r.ID, Offset: 0, Data: body[:10]}); err == nil {
		t.Fatal("wrong offset accepted")
	}
	s.Chunk(proto.ChunkArgs{ID: r.ID, Offset: 65536, Data: body[65536:131072]})
	s.Chunk(proto.ChunkArgs{ID: r.ID, Offset: 131072, Data: body[131072:]})
	final, err := s.Done(r.ID)
	if err != nil || final != "Αίθουσα Α/talk.pptx" {
		t.Fatalf("done %q %v", final, err)
	}
	got, _ := os.ReadFile(filepath.Join(s.Root(), "Αίθουσα Α", "talk.pptx"))
	if sum(got) != sum(body) {
		t.Fatal("content differs")
	}
	// Same name again: becomes " (2)", never overwrites.
	r3, _ := s.Offer(proto.OfferArgs{Path: "Αίθουσα Α/talk.pptx", Size: 3, SHA256: sum([]byte("abc"))})
	s.Chunk(proto.ChunkArgs{ID: r3.ID, Data: []byte("abc")})
	final, err = s.Done(r3.ID)
	if err != nil || final != "Αίθουσα Α/talk (2).pptx" {
		t.Fatalf("unique %q %v", final, err)
	}
}

func TestHashMismatch(t *testing.T) {
	s := newStore(t)
	r, _ := s.Offer(proto.OfferArgs{Path: "x.pdf", Size: 3, SHA256: sum([]byte("abc"))})
	s.Chunk(proto.ChunkArgs{ID: r.ID, Data: []byte("abd")})
	if _, err := s.Done(r.ID); CodeOf(err) != proto.ErrHashMismatch {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "x.pdf")); err == nil {
		t.Fatal("damaged file kept")
	}
}

func TestListRenameDeleteInUse(t *testing.T) {
	s := newStore(t)
	os.MkdirAll(filepath.Join(s.Root(), "d"), 0o755)
	os.WriteFile(filepath.Join(s.Root(), "d", "a.pdf"), []byte("x"), 0o644)
	l, err := s.List()
	if err != nil || len(l) != 2 || l[0].Path != "d" || !l[0].Dir || l[1].Path != "d/a.pdf" {
		t.Fatalf("list %+v %v", l, err)
	}
	s.SetOpen("d/a.pdf")
	if _, err := s.Rename("d", "e"); CodeOf(err) != proto.ErrInUse {
		t.Fatalf("rename in use: %v", err)
	}
	if err := s.Delete("d/a.pdf"); CodeOf(err) != proto.ErrInUse {
		t.Fatalf("delete in use: %v", err)
	}
	s.SetOpen("")
	to, err := s.Rename("d/a.pdf", "b.pdf")
	if err != nil || to != "b.pdf" {
		t.Fatalf("rename %q %v", to, err)
	}
	if err := s.Delete("b.pdf"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "b.pdf")); err == nil {
		t.Fatal("still there")
	}
}

func TestSymlinkEscape(t *testing.T) {
	s := newStore(t)
	out := t.TempDir()
	if err := os.Symlink(out, filepath.Join(s.Root(), "link")); err != nil {
		t.Skip("no symlinks here")
	}
	if _, err := s.Offer(proto.OfferArgs{Path: "link/x.pdf", Size: 1, SHA256: sum([]byte("a"))}); err == nil {
		t.Fatal("wrote through a link out of the folder")
	}
}
