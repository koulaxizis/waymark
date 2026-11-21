// Package files is the Agent folder: the one place on the presenter laptop that
// Waymark reads and writes. Every path that comes over the wire is relative to
// it and is checked before use, so nothing outside the folder can be reached.
//
// Uploads land in a hidden partial area first, resume after a dropped
// connection, and only appear under their name once the SHA-256 matches. A name
// that already exists gets " (2)", " (3)" and so on: nothing is ever overwritten
// silently. Deletes go to the system Recycle Bin / Trash, not away for good.
package files

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/koulaxizis/waymark/app/internal/proto"
)

// Error carries a protocol error code.
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

func errf(code, f string, a ...any) error { return &Error{Code: code, Msg: fmt.Sprintf(f, a...)} }

// CodeOf returns the protocol code of err, or IO.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	if errors.Is(err, fs.ErrNotExist) {
		return proto.ErrNotFound
	}
	return proto.ErrIO
}

// internalDir holds partial uploads. It never shows in listings.
const internalDir = ".waymark"

// MaxEntries caps a listing so a huge folder cannot stall the channel.
const MaxEntries = 5000

// Store is the Agent folder.
type Store struct {
	root string
	mu   sync.Mutex
	// open is the path of the presentation in use, which the secretariat may
	// not rename or delete.
	open string
}

// New opens (and creates) the Agent folder.
func New(root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(abs, internalDir, "partial"), 0o700); err != nil {
		return nil, err
	}
	hideDir(filepath.Join(abs, internalDir))
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	return &Store{root: abs}, nil
}

// Root is the absolute folder path.
func (s *Store) Root() string { return s.root }

// SetOpen records the presentation in use ("" for none).
func (s *Store) SetOpen(rel string) {
	s.mu.Lock()
	s.open = rel
	s.mu.Unlock()
}

// IsOpen reports whether rel is, or contains, the presentation in use.
func (s *Store) IsOpen(rel string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open == "" {
		return false
	}
	return s.open == rel || strings.HasPrefix(s.open, rel+"/")
}

// Clean checks a wire path and returns it in canonical form (forward slashes,
// no dot segments). It refuses absolute paths, parent segments, drive letters,
// the internal folder, and names Windows cannot hold.
func Clean(rel string) (string, error) {
	rel = strings.ReplaceAll(rel, "\\", "/")
	if rel == "" || strings.HasPrefix(rel, "/") || strings.Contains(rel, ":") || strings.ContainsRune(rel, 0) {
		return "", errf(proto.ErrBadRequest, "invalid path %q", rel)
	}
	parts := strings.Split(rel, "/")
	out := parts[:0]
	for _, p := range parts {
		switch {
		case p == "" || p == ".":
			continue
		case p == "..":
			return "", errf(proto.ErrBadRequest, "path leaves the folder")
		case !validName(p):
			return "", errf(proto.ErrBadRequest, "invalid name %q", p)
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return "", errf(proto.ErrBadRequest, "empty path")
	}
	if strings.EqualFold(out[0], internalDir) {
		return "", errf(proto.ErrBadRequest, "reserved name")
	}
	return strings.Join(out, "/"), nil
}

var winReserved = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true}

func validName(p string) bool {
	if len(p) > 255 || strings.HasSuffix(p, ".") || strings.HasSuffix(p, " ") {
		return false
	}
	for _, r := range p {
		if r < 32 || strings.ContainsRune(`<>:"|?*`, r) || r == unicode.ReplacementChar {
			return false
		}
	}
	base := strings.ToUpper(strings.SplitN(p, ".", 2)[0])
	return !winReserved[base]
}

// abs turns a clean relative path into an absolute one inside the root, and
// refuses it if a symlink or junction on the way leads outside.
func (s *Store) abs(rel string) (string, error) {
	p := filepath.Join(s.root, filepath.FromSlash(rel))
	// Resolve the deepest existing ancestor; it must stay inside the root.
	q := p
	for {
		if real, err := filepath.EvalSymlinks(q); err == nil {
			if !inside(s.root, real) {
				return "", errf(proto.ErrBadRequest, "path leaves the folder")
			}
			break
		}
		parent := filepath.Dir(q)
		if parent == q {
			break
		}
		q = parent
	}
	return p, nil
}

func inside(root, p string) bool {
	r, err := filepath.Rel(root, p)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r)
}

// List returns every file and folder under the root, folders first.
func (s *Store) List() ([]proto.Entry, error) {
	var out []proto.Entry
	err := filepath.WalkDir(s.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if p == s.root {
			return nil
		}
		rel, _ := filepath.Rel(s.root, p)
		rel = filepath.ToSlash(rel)
		if strings.EqualFold(strings.SplitN(rel, "/", 2)[0], internalDir) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // never follow links out of the folder
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		e := proto.Entry{Path: rel, Dir: d.IsDir(), MTime: info.ModTime().Unix()}
		if !d.IsDir() {
			e.Size = info.Size()
		}
		e.Open = s.IsOpen(rel)
		out = append(out, e)
		if len(out) >= MaxEntries {
			return fs.SkipAll
		}
		return nil
	})
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	return out, err
}

// partial is the on-disk record of an upload in progress.
type partial struct {
	Path   string `json:"path"` // requested path
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func partialID(p partial) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s", strings.ToLower(p.Path), p.Size, p.SHA256)))
	return hex.EncodeToString(h[:8])
}

func (s *Store) partialPaths(id string) (data, meta string) {
	base := filepath.Join(s.root, internalDir, "partial", id)
	return base + ".part", base + ".json"
}

// Offer starts or resumes an upload. The reply says where to continue.
func (s *Store) Offer(a proto.OfferArgs) (proto.OfferReply, error) {
	rel, err := Clean(a.Path)
	if err != nil {
		return proto.OfferReply{}, err
	}
	if a.Size < 0 || a.Size > proto.MaxFileSize {
		return proto.OfferReply{}, errf(proto.ErrBadRequest, "file larger than 2 GB")
	}
	sum := strings.ToLower(a.SHA256)
	if b, err := hex.DecodeString(sum); err != nil || len(b) != 32 {
		return proto.OfferReply{}, errf(proto.ErrBadRequest, "sha256 must be 64 hex characters")
	}
	if _, err := s.abs(rel); err != nil {
		return proto.OfferReply{}, err
	}
	p := partial{Path: rel, Size: a.Size, SHA256: sum}
	id := partialID(p)
	data, meta := s.partialPaths(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	var off int64
	if st, err := os.Stat(data); err == nil && st.Size() <= a.Size {
		off = st.Size()
	} else {
		if err := os.WriteFile(data, nil, 0o600); err != nil {
			return proto.OfferReply{}, err
		}
	}
	mb, _ := json.Marshal(p)
	if err := os.WriteFile(meta, mb, 0o600); err != nil {
		return proto.OfferReply{}, err
	}
	final, err := s.uniqueLocked(rel)
	if err != nil {
		return proto.OfferReply{}, err
	}
	return proto.OfferReply{ID: id, Path: final, Offset: off}, nil
}

func (s *Store) loadPartial(id string) (partial, string, error) {
	if len(id) != 16 || strings.Trim(id, "0123456789abcdef") != "" {
		return partial{}, "", errf(proto.ErrBadRequest, "bad transfer id")
	}
	data, meta := s.partialPaths(id)
	b, err := os.ReadFile(meta)
	if err != nil {
		return partial{}, "", errf(proto.ErrNotFound, "unknown transfer, offer the file again")
	}
	var p partial
	if err := json.Unmarshal(b, &p); err != nil {
		return partial{}, "", err
	}
	return p, data, nil
}

// Chunk appends data at offset. Offset must equal what is already stored.
func (s *Store) Chunk(a proto.ChunkArgs) (int64, error) {
	if len(a.Data) > proto.ChunkSize {
		return 0, errf(proto.ErrBadRequest, "chunk larger than 64 KB")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, data, err := s.loadPartial(a.ID)
	if err != nil {
		return 0, err
	}
	f, err := os.OpenFile(data, os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if st.Size() != a.Offset {
		return st.Size(), errf(proto.ErrBadRequest, "offset %d, expected %d", a.Offset, st.Size())
	}
	if st.Size()+int64(len(a.Data)) > p.Size {
		return st.Size(), errf(proto.ErrBadRequest, "more data than offered")
	}
	if _, err := f.WriteAt(a.Data, a.Offset); err != nil {
		return st.Size(), err
	}
	return a.Offset + int64(len(a.Data)), nil
}

// Done checks the hash and moves the file to its final name, which it returns.
func (s *Store) Done(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, data, err := s.loadPartial(id)
	if err != nil {
		return "", err
	}
	_, meta := s.partialPaths(id)
	sum, size, err := hashFile(data)
	if err != nil {
		return "", err
	}
	if size != p.Size {
		return "", errf(proto.ErrBadRequest, "file incomplete: %d of %d bytes", size, p.Size)
	}
	if sum != p.SHA256 {
		os.Remove(data)
		os.Remove(meta)
		return "", errf(proto.ErrHashMismatch, "the file arrived damaged, send it again")
	}
	final, err := s.uniqueLocked(p.Path)
	if err != nil {
		return "", err
	}
	dst, err := s.abs(final)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(data, dst); err != nil {
		return "", err
	}
	os.Remove(meta)
	return final, nil
}

// uniqueLocked returns rel, or rel with " (n)" before the extension, so that
// nothing existing is overwritten.
func (s *Store) uniqueLocked(rel string) (string, error) {
	dir, name := path.Split(rel)
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 1; n < 1000; n++ {
		cand := rel
		if n > 1 {
			cand = dir + fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		p, err := s.abs(cand)
		if err != nil {
			return "", err
		}
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			return cand, nil
		}
	}
	return "", errf(proto.ErrBusy, "too many files named %q", name)
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// Hash returns the SHA-256 and size of a file in the folder.
func (s *Store) Hash(rel string) (string, int64, error) {
	p, err := s.Path(rel)
	if err != nil {
		return "", 0, err
	}
	return hashFile(p)
}

// Path checks rel and returns the absolute path of an existing regular file.
func (s *Store) Path(rel string) (string, error) {
	rel, err := Clean(rel)
	if err != nil {
		return "", err
	}
	p, err := s.abs(rel)
	if err != nil {
		return "", err
	}
	st, err := os.Lstat(p)
	if err != nil {
		return "", errf(proto.ErrNotFound, "%s not found", rel)
	}
	if !st.Mode().IsRegular() {
		return "", errf(proto.ErrBadRequest, "%s is not a file", rel)
	}
	return p, nil
}

// Rel turns an absolute path inside the folder back into a wire path.
func (s *Store) Rel(abs string) string {
	r, err := filepath.Rel(s.root, abs)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(r)
}

// Mkdir creates a folder (and its parents).
func (s *Store) Mkdir(rel string) (string, error) {
	rel, err := Clean(rel)
	if err != nil {
		return "", err
	}
	p, err := s.abs(rel)
	if err != nil {
		return "", err
	}
	return rel, os.MkdirAll(p, 0o755)
}

// Rename moves a file or folder inside the folder. It never overwrites.
func (s *Store) Rename(from, to string) (string, error) {
	from, err := Clean(from)
	if err != nil {
		return "", err
	}
	to, err = Clean(to)
	if err != nil {
		return "", err
	}
	if s.IsOpen(from) {
		return "", errf(proto.ErrInUse, "%s is the presentation in use", from)
	}
	src, err := s.abs(from)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(src); err != nil {
		return "", errf(proto.ErrNotFound, "%s not found", from)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.EqualFold(from, to) && from != to && runtime.GOOS != "linux" {
		// Case-only rename on a case-insensitive file system.
		dst, _ := s.abs(to)
		return to, os.Rename(src, dst)
	}
	final, err := s.uniqueLocked(to)
	if err != nil {
		return "", err
	}
	dst, err := s.abs(final)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	return final, os.Rename(src, dst)
}

// Delete sends a file or folder to the Recycle Bin / Trash.
func (s *Store) Delete(rel string) error {
	rel, err := Clean(rel)
	if err != nil {
		return err
	}
	if s.IsOpen(rel) {
		return errf(proto.ErrInUse, "%s is the presentation in use", rel)
	}
	p, err := s.abs(rel)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(p); err != nil {
		return errf(proto.ErrNotFound, "%s not found", rel)
	}
	if err := moveToTrash(p); err != nil {
		// Last resort: keep it, hidden, so it is never lost for good.
		dst := filepath.Join(s.root, internalDir, "trash", filepath.Base(p))
		os.MkdirAll(filepath.Dir(dst), 0o700)
		return os.Rename(p, uniqueAbs(dst))
	}
	return nil
}

func uniqueAbs(p string) string {
	ext := filepath.Ext(p)
	stem := strings.TrimSuffix(p, ext)
	for n := 1; ; n++ {
		cand := p
		if n > 1 {
			cand = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		if _, err := os.Lstat(cand); errors.Is(err, fs.ErrNotExist) {
			return cand
		}
	}
}

// Open returns a reader for a download, with its size and hash.
func (s *Store) Open(rel string) (*os.File, int64, string, error) {
	p, err := s.Path(rel)
	if err != nil {
		return nil, 0, "", err
	}
	sum, size, err := hashFile(p)
	if err != nil {
		return nil, 0, "", err
	}
	f, err := os.Open(p)
	return f, size, sum, err
}

// Clear removes everything the Agent received: used when the Agent closes and
// the presenter agrees. It empties only the default folder Waymark created.
func (s *Store) Clear() error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	var first error
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(s.root, e.Name())); err != nil && first == nil {
			first = err
		}
	}
	os.MkdirAll(filepath.Join(s.root, internalDir, "partial"), 0o700)
	return first
}
