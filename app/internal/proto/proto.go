// Package proto is the Waymark Remote wire protocol: the message envelope, the
// closed command list, the error codes, the roles and what each role may do.
//
// Every message travels as JSON inside the encrypted channel. A command carries
// an id and a rising seq; the Agent answers each one with "ok" or "error", and
// ignores a presentation command whose seq is not newer than the last one it ran
// for that peer, so a double tap or a late retransmission never skips two slides.
package proto

import (
	"encoding/json"
	"strings"
)

// Version is the protocol version. Both sides check it at pairing.
const Version = 1

// Message types.
const (
	TCmd      = "cmd"       // Controller -> Agent
	TOK       = "ok"        // Agent -> Controller, answer to a cmd
	TError    = "error"     // Agent -> Controller, answer to a cmd
	TState    = "state"     // Agent -> all, on every change and every 5 s
	TEvent    = "event"     // Agent -> all, e.g. a new file from the secretariat
	TFileData = "file.data" // Agent -> one Controller, a download chunk
	TBye      = "bye"       // either side, closing on purpose
)

// Commands. The Agent runs these and nothing else: no shell, no arbitrary files.
const (
	CmdPing        = "ping"
	CmdStateGet    = "state.get"
	CmdControlTake = "control.take"
	CmdControlFree = "control.release"
	CmdFileList    = "file.list"
	CmdFileOffer   = "file.offer"
	CmdFileChunk   = "file.chunk"
	CmdFileDone    = "file.done"
	CmdFileGet     = "file.get"
	CmdFileMkdir   = "file.mkdir"
	CmdFileRename  = "file.rename"
	CmdFileDelete  = "file.delete"
	CmdPresOpen    = "pres.open"
	CmdPresStart   = "pres.start"
	CmdPresNext    = "pres.next"
	CmdPresPrev    = "pres.prev"
	CmdPresGoto    = "pres.goto"
	CmdPresBlack   = "pres.black"
	CmdPresEnd     = "pres.end"
	CmdPresClose   = "pres.close"
)

// Error codes. The Controller shows each one as a sentence and what to do.
const (
	ErrAppNotFound  = "APP_NOT_FOUND"
	ErrPermission   = "PERMISSION_NEEDED"
	ErrUnsupported  = "UNSUPPORTED"
	ErrHashMismatch = "HASH_MISMATCH"
	ErrNotHolder    = "NOT_HOLDER"
	ErrNotAllowed   = "NOT_ALLOWED"
	ErrFileType     = "FILE_TYPE"
	ErrBusy         = "BUSY"
	ErrBadRequest   = "BAD_REQUEST"
	ErrNotFound     = "NOT_FOUND"
	ErrInUse        = "IN_USE"
	ErrStale        = "STALE"
	ErrUnknown      = "UNKNOWN_COMMAND"
	ErrIO           = "IO"
	ErrVersion      = "VERSION"
)

// Msg is the single envelope for everything on the channel.
type Msg struct {
	V    int             `json:"v"`
	T    string          `json:"t"`
	ID   string          `json:"id,omitempty"`
	Seq  uint64          `json:"seq,omitempty"`
	Cmd  string          `json:"cmd,omitempty"`
	Args json.RawMessage `json:"args,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
	Code string          `json:"code,omitempty"`
	Err  string          `json:"err,omitempty"`
}

// Role is chosen at pairing. The Agent may lower it, never raise it.
type Role string

const (
	RoleSecretariat Role = "secretariat" // Γραμματεία
	RoleOperator    Role = "operator"    // Χειριστής
	RoleSupervisor  Role = "supervisor"  // Εποπτεία
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	return r == RoleSecretariat || r == RoleOperator || r == RoleSupervisor
}

// Rank orders roles so the Agent can only lower one.
func (r Role) Rank() int {
	switch r {
	case RoleSecretariat:
		return 1
	case RoleOperator:
		return 2
	case RoleSupervisor:
		return 3
	}
	return 0
}

// IsPres reports a command that drives the presentation.
func IsPres(cmd string) bool { return strings.HasPrefix(cmd, "pres.") }

// NeedsHolder reports a command that only the holder of the control key may run.
func NeedsHolder(cmd string) bool { return IsPres(cmd) }

// Allowed is the role table of the Doc ("Ενιαίο μοντέλο με ρόλους"). The Agent
// checks it on every command, not only the Controller UI.
func Allowed(r Role, cmd string) bool {
	switch cmd {
	case CmdPing, CmdStateGet, CmdFileList, CmdFileOffer, CmdFileChunk, CmdFileDone,
		CmdFileGet, CmdFileMkdir, CmdFileRename, CmdFileDelete:
		return r.Valid() // the secretariat may not rename or delete a file in use; the Agent checks that
	case CmdControlTake, CmdControlFree:
		return r == RoleOperator || r == RoleSupervisor
	}
	if IsPres(cmd) {
		return r == RoleOperator || r == RoleSupervisor
	}
	return false
}

// Presentation states.
const (
	StatusIdle    = "idle"
	StatusOpen    = "open"
	StatusShowing = "showing"
	StatusBlack   = "black"
)

// Peer is one connected Controller as the Agent sees it.
type Peer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role Role   `json:"role"`
}

// State is what the Agent reports on every change and every 5 s.
type State struct {
	Device  string `json:"device"`
	Agent   string `json:"agent"` // Agent version
	OS      string `json:"os"`
	App     string `json:"app,omitempty"`  // powerpoint, impress, viewer, ...
	File    string `json:"file,omitempty"` // path inside the Agent folder
	FileSHA string `json:"fileSha,omitempty"`
	Slide   int    `json:"slide"`  // 0 = unknown
	Slides  int    `json:"slides"` // 0 = unknown
	Status  string `json:"status"`
	Holder  *Peer  `json:"holder,omitempty"`
	Peers   []Peer `json:"peers"`
	Folder  string `json:"folder"` // display name of the Agent folder
}

// Entry is one file or folder in a listing. Path is relative to the Agent
// folder and always uses forward slashes.
type Entry struct {
	Path  string `json:"path"`
	Dir   bool   `json:"dir,omitempty"`
	Size  int64  `json:"size,omitempty"`
	MTime int64  `json:"mtime"`          // Unix seconds
	Open  bool   `json:"open,omitempty"` // the presentation in use
}

// Arguments of the commands that take any.

type OfferArgs struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"` // hex
}

// OfferReply tells the sender where to resume.
type OfferReply struct {
	ID     string `json:"id"`
	Path   string `json:"path"` // final name, may carry " (2)"
	Offset int64  `json:"offset"`
}

type ChunkArgs struct {
	ID     string `json:"id"`
	Offset int64  `json:"offset"`
	Data   []byte `json:"data"` // base64 in JSON
}

type DoneArgs struct {
	ID string `json:"id"`
}

type GetArgs struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
}

// GetReply starts a download; file.data messages follow with the same id.
type GetReply struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// FileData is one download chunk. Last marks the end.
type FileData struct {
	Offset int64  `json:"offset"`
	Data   []byte `json:"data"`
	Last   bool   `json:"last,omitempty"`
}

type PathArgs struct {
	Path string `json:"path"`
}

type RenameArgs struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type OpenArgs struct {
	Path string `json:"path"`
	App  string `json:"app,omitempty"` // "" or "auto", or a driver name
}

type StartArgs struct {
	From int `json:"from,omitempty"`
}

type GotoArgs struct {
	Slide int `json:"slide"`
}

type BlackArgs struct {
	On bool `json:"on"`
}

type PingArgs struct {
	T0 int64 `json:"t0"`
}

// Event is pushed by the Agent.
type Event struct {
	Kind string `json:"kind"` // file.new, control.taken, disconnect
	Path string `json:"path,omitempty"`
	By   *Peer  `json:"by,omitempty"`
}

// ChunkSize is the transfer unit.
const ChunkSize = 64 << 10

// OpenableExt is the closed list of types the Agent opens. Transfer accepts any
// type; the Agent never executes a file.
var OpenableExt = map[string]bool{".pptx": true, ".ppt": true, ".key": true, ".odp": true, ".pdf": true}

// MaxFileSize per file.
const MaxFileSize = 2 << 30
