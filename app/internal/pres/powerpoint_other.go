//go:build !windows

package pres

import "github.com/koulaxizis/waymark/app/internal/proto"

// PowerPoint on macOS (AppleScript) comes in a later version.
func PowerPoint() Opener {
	return Opener{
		Name: "powerpoint",
		Exts: []string{".pptx", ".ppt"},
		Open: func(string) (Driver, error) {
			return nil, Errf(proto.ErrAppNotFound, "PowerPoint control is not available on this system yet")
		},
	}
}
