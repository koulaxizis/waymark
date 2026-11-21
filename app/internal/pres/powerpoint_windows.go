package pres

import (
	"fmt"
	"runtime"
	"strings"
	"sync"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"

	"github.com/koulaxizis/waymark/app/internal/proto"
)

// PowerPoint constants (Office type library).
const (
	msoTrue                 = -1
	msoFalse                = 0
	ppShowAll               = 1
	ppShowSlideRange        = 2
	ppSlideShowRunning      = 1
	ppSlideShowBlackScreen  = 3
	ppAdvanceOnClick        = 1
	ppShowTypeSpeaker       = 1
	ppSlideShowPointerArrow = 1
)

// PowerPoint drives an installed Microsoft PowerPoint through COM. It needs
// no admin rights. All COM calls run on one locked OS thread, as COM requires.
func PowerPoint() Opener {
	return Opener{
		Name: "powerpoint",
		Exts: []string{".pptx", ".ppt", ".odp"},
		Open: openPowerPoint,
	}
}

type ppDriver struct {
	calls chan func()
	app   *ole.IDispatch
	pres  *ole.IDispatch
	// ownApp: PowerPoint was not running before, so Close may quit it.
	ownApp bool

	mu     sync.Mutex
	closed bool
}

func openPowerPoint(path string) (Driver, error) {
	d := &ppDriver{calls: make(chan func())}
	ready := make(chan error, 1)
	go d.loop(path, ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return d, nil
}

func (d *ppDriver) loop(path string, ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		if oe, ok := err.(*ole.OleError); !ok || oe.Code() != 1 { // S_FALSE: already initialised
			ready <- Errf(proto.ErrIO, "COM: "+err.Error())
			return
		}
	}
	defer ole.CoUninitialize()
	if err := d.open(path); err != nil {
		ready <- err
		return
	}
	ready <- nil
	for f := range d.calls {
		f()
	}
}

func (d *ppDriver) open(path string) error {
	// Attach to a running PowerPoint when there is one, so the presenter's
	// other open files stay as they are.
	if unk, err := oleutil.GetActiveObject("PowerPoint.Application"); err == nil {
		d.app, _ = unk.QueryInterface(ole.IID_IDispatch)
		unk.Release()
	}
	if d.app == nil {
		unk, err := oleutil.CreateObject("PowerPoint.Application")
		if err != nil {
			return Errf(proto.ErrAppNotFound, "PowerPoint is not installed on this laptop")
		}
		d.app, err = unk.QueryInterface(ole.IID_IDispatch)
		unk.Release()
		if err != nil {
			return Errf(proto.ErrAppNotFound, "PowerPoint did not start")
		}
		d.ownApp = true
	}
	oleutil.PutProperty(d.app, "Visible", msoTrue)
	pres, err := oleutil.GetProperty(d.app, "Presentations")
	if err != nil {
		return Errf(proto.ErrAppNotFound, "PowerPoint: "+err.Error())
	}
	defer pres.Clear()
	// Open(FileName, ReadOnly, Untitled, WithWindow). ReadOnly keeps the
	// presenter's file untouched.
	v, err := oleutil.CallMethod(pres.ToIDispatch(), "Open", path, msoTrue, msoFalse, msoTrue)
	if err != nil {
		msg := err.Error()
		if strings.Contains(strings.ToLower(msg), "protected") {
			return Errf(proto.ErrPermission, "PowerPoint opened the file in Protected View; allow editing on the laptop")
		}
		return Errf(proto.ErrIO, "PowerPoint could not open the file: "+msg)
	}
	d.pres = v.ToIDispatch()
	return nil
}

// call runs f on the COM thread and waits.
func (d *ppDriver) call(f func() error) (err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return Errf(proto.ErrBadRequest, "no presentation is open")
	}
	done := make(chan struct{})
	d.calls <- func() {
		defer func() {
			if r := recover(); r != nil {
				err = Errf(proto.ErrIO, fmt.Sprint("PowerPoint: ", r))
			}
			close(done)
		}()
		err = f()
	}
	<-done
	return err
}

func (d *ppDriver) Name() string { return "powerpoint" }

// view returns the slide show view, or nil when no show runs.
func (d *ppDriver) view() *ole.IDispatch {
	w, err := oleutil.GetProperty(d.pres, "SlideShowWindow")
	if err != nil {
		return nil
	}
	wd := w.ToIDispatch()
	defer wd.Release()
	v, err := oleutil.GetProperty(wd, "View")
	if err != nil {
		return nil
	}
	return v.ToIDispatch()
}

func (d *ppDriver) withView(f func(v *ole.IDispatch) error) error {
	return d.call(func() error {
		v := d.view()
		if v == nil {
			return Errf(proto.ErrBadRequest, "the slide show is not running; press Start")
		}
		defer v.Release()
		return f(v)
	})
}

func (d *ppDriver) Start(from int) error {
	return d.call(func() error {
		if v := d.view(); v != nil {
			defer v.Release()
			if from > 0 {
				_, err := oleutil.CallMethod(v, "GotoSlide", from)
				return err
			}
			return nil
		}
		s, err := oleutil.GetProperty(d.pres, "SlideShowSettings")
		if err != nil {
			return err
		}
		st := s.ToIDispatch()
		defer st.Release()
		oleutil.PutProperty(st, "ShowType", ppShowTypeSpeaker)
		oleutil.PutProperty(st, "AdvanceMode", ppAdvanceOnClick)
		if from > 1 {
			oleutil.PutProperty(st, "RangeType", ppShowSlideRange)
			oleutil.PutProperty(st, "StartingSlide", from)
			oleutil.PutProperty(st, "EndingSlide", d.count())
		} else {
			oleutil.PutProperty(st, "RangeType", ppShowAll)
		}
		w, err := oleutil.CallMethod(st, "Run")
		if err != nil {
			return Errf(proto.ErrIO, "PowerPoint did not start the show: "+err.Error())
		}
		w.Clear()
		return nil
	})
}

func (d *ppDriver) count() int {
	s, err := oleutil.GetProperty(d.pres, "Slides")
	if err != nil {
		return 0
	}
	sd := s.ToIDispatch()
	defer sd.Release()
	n, err := oleutil.GetProperty(sd, "Count")
	if err != nil {
		return 0
	}
	return int(n.Val)
}

func (d *ppDriver) Next() error {
	return d.withView(func(v *ole.IDispatch) error { _, err := oleutil.CallMethod(v, "Next"); return err })
}

func (d *ppDriver) Prev() error {
	return d.withView(func(v *ole.IDispatch) error { _, err := oleutil.CallMethod(v, "Previous"); return err })
}

func (d *ppDriver) Goto(n int) error {
	return d.withView(func(v *ole.IDispatch) error {
		if c := d.count(); c > 0 && n > c {
			return Errf(proto.ErrBadRequest, fmt.Sprintf("the presentation has %d slides", c))
		}
		_, err := oleutil.CallMethod(v, "GotoSlide", n)
		return err
	})
}

func (d *ppDriver) Black(on bool) error {
	return d.withView(func(v *ole.IDispatch) error {
		st := ppSlideShowRunning
		if on {
			st = ppSlideShowBlackScreen
		}
		_, err := oleutil.PutProperty(v, "State", st)
		return err
	})
}

func (d *ppDriver) End() error {
	return d.call(func() error {
		if v := d.view(); v != nil {
			defer v.Release()
			_, err := oleutil.CallMethod(v, "Exit")
			return err
		}
		return nil
	})
}

func (d *ppDriver) Close() error {
	err := d.call(func() error {
		if v := d.view(); v != nil {
			oleutil.CallMethod(v, "Exit")
			v.Release()
		}
		if d.pres != nil {
			oleutil.CallMethod(d.pres, "Close")
			d.pres.Release()
			d.pres = nil
		}
		if d.ownApp {
			// Quit only if no other presentation is open.
			if p, err := oleutil.GetProperty(d.app, "Presentations"); err == nil {
				pd := p.ToIDispatch()
				if n, err := oleutil.GetProperty(pd, "Count"); err == nil && n.Val == 0 {
					oleutil.CallMethod(d.app, "Quit")
				}
				pd.Release()
			}
		}
		d.app.Release()
		return nil
	})
	d.mu.Lock()
	d.closed = true
	close(d.calls)
	d.mu.Unlock()
	return err
}

func (d *ppDriver) Status() Status {
	var st Status
	d.call(func() error {
		st.Slides = d.count()
		st.State = proto.StatusOpen
		v := d.view()
		if v == nil {
			return nil
		}
		defer v.Release()
		st.State = proto.StatusShowing
		if s, err := oleutil.GetProperty(v, "State"); err == nil && s.Val == ppSlideShowBlackScreen {
			st.State = proto.StatusBlack
		}
		if p, err := oleutil.GetProperty(v, "CurrentShowPosition"); err == nil {
			st.Slide = int(p.Val)
		}
		return nil
	})
	return st
}
