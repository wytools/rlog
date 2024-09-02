package rotation

import (
	"context"
	"encoding"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type DefaultHandler struct {
	opts              slog.HandlerOptions
	preformattedAttrs []byte
	// groupPrefix is for the text handler only.
	// It holds the prefix for groups that were already pre-formatted.
	// A group will appear here when a call to WithGroup is followed by
	// a call to WithAttrs.
	groupPrefix string
	groups      []string // all groups started from WithGroup
	nOpenGroups int      // the number of groups opened in preformattedAttrs
	mu          *sync.Mutex
	w           io.Writer
}

func NewDefaultHandler(w io.Writer, opts *slog.HandlerOptions) *DefaultHandler {
	return &DefaultHandler{
		w:    w,
		opts: *opts,
		mu:   &sync.Mutex{},
	}
}

func (h *DefaultHandler) Enabled(ctx context.Context, l slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.opts.Level != nil {
		minLevel = h.opts.Level.Level()
	}
	return l >= minLevel
}

func (h *DefaultHandler) Handle(ctx context.Context, r slog.Record) error {
	state := h.newHandleState(NewBuffer(), true, " ")
	defer state.free()

	// Built-in attributes. They are not in a group.
	stateGroups := state.groups
	state.groups = nil // So ReplaceAttrs sees no groups instead of the pre groups.
	// time
	if !r.Time.IsZero() {
		state.appendTime(r.Time.Round(0))
		state.appendSep()
	}
	// level
	state.appendString(r.Level.String())
	state.appendSep()

	// source
	if h.opts.AddSource {
		if r.Level == slog.LevelDebug || r.Level == slog.LevelError {
			state.appendAttr(slog.Any(slog.SourceKey, source(&r)))
			state.appendSep()
		}
	}

	// msg
	state.appendString(r.Message)

	// groups
	state.groups = stateGroups // Restore groups passed to ReplaceAttrs.
	state.appendNonBuiltIns(r)
	state.buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(*state.buf)
	return err
}

func (h *DefaultHandler) WithAttrs(as []slog.Attr) slog.Handler {
	// We are going to ignore empty groups, so if the entire slice consists of
	// them, there is nothing to do.
	if countEmptyGroups(as) == len(as) {
		return h
	}
	h2 := h.clone()
	// Pre-format the attributes as an optimization.
	state := h2.newHandleState((*Buffer)(&h2.preformattedAttrs), false, "")
	defer state.free()
	state.prefix.WriteString(h.groupPrefix)
	if len(h2.preformattedAttrs) > 0 {
		state.sep = h.attrSep()
	}
	state.openGroups()
	for _, a := range as {
		state.appendAttr(a)
	}
	// Remember the new prefix for later keys.
	h2.groupPrefix = state.prefix.String()
	// Remember how many opened groups are in preformattedAttrs,
	// so we don't open them again when we handle a Record.
	h2.nOpenGroups = len(h2.groups)
	return h2
}

func (h *DefaultHandler) WithGroup(name string) slog.Handler {
	h2 := h.clone()
	h2.groups = append(h2.groups, name)
	return h2
}

func (h *DefaultHandler) clone() *DefaultHandler {
	// We can't use assignment because we can't copy the mutex.
	return &DefaultHandler{
		opts:              h.opts,
		preformattedAttrs: slices.Clip(h.preformattedAttrs),
		groupPrefix:       h.groupPrefix,
		groups:            slices.Clip(h.groups),
		nOpenGroups:       h.nOpenGroups,
		w:                 h.w,
		mu:                h.mu, // mutex shared among all clones of this handler
	}
}

// attrSep returns the separator between attributes.
func (h *DefaultHandler) attrSep() string {
	return " "
}

var groupPool = sync.Pool{New: func() any {
	s := make([]string, 0, 10)
	return &s
}}

func (h *DefaultHandler) newHandleState(buf *Buffer, freeBuf bool, sep string) handleState {
	s := handleState{
		h:       h,
		buf:     buf,
		freeBuf: freeBuf,
		sep:     sep,
		prefix:  NewBuffer(),
	}
	if h.opts.ReplaceAttr != nil {
		s.groups = groupPool.Get().(*[]string)
		*s.groups = append(*s.groups, h.groups[:h.nOpenGroups]...)
	}
	return s
}

// source returns a Source for the log event.
// If the Record was created without the necessary information,
// or if the location is unavailable, it returns a non-nil *Source
// with zero fields.
func source(r *slog.Record) *slog.Source {
	fs := runtime.CallersFrames([]uintptr{r.PC})
	f, _ := fs.Next()
	return &slog.Source{
		Function: f.Function,
		File:     f.File,
		Line:     f.Line,
	}
}

// handleState holds state for a single call to commonHandler.handle.
// The initial value of sep determines whether to emit a separator
// before the next key, after which it stays true.
type handleState struct {
	h       *DefaultHandler
	buf     *Buffer
	freeBuf bool      // should buf be freed?
	sep     string    // separator to write before next key
	prefix  *Buffer   // for text: key prefix
	groups  *[]string // pool-allocated slice of active groups, for ReplaceAttr
}

func (s *handleState) free() {
	if s.freeBuf {
		s.buf.Free()
	}
	if gs := s.groups; gs != nil {
		*gs = (*gs)[:0]
		groupPool.Put(gs)
	}
	s.prefix.Free()
}

func (s *handleState) openGroups() {
	for _, n := range s.h.groups[s.h.nOpenGroups:] {
		s.openGroup(n)
	}
}

// Separator for group names and keys.
const keyComponentSep = '.'

// openGroup starts a new group of attributes
// with the given name.
func (s *handleState) openGroup(name string) {
	s.prefix.WriteString(name)
	s.prefix.WriteByte(keyComponentSep)
	// Collect group names for ReplaceAttr.
	if s.groups != nil {
		*s.groups = append(*s.groups, name)
	}
}

// closeGroup ends the group with the given name.
func (s *handleState) closeGroup(name string) {
	(*s.prefix) = (*s.prefix)[:len(*s.prefix)-len(name)-1 /* for keyComponentSep */]
	if s.groups != nil {
		*s.groups = (*s.groups)[:len(*s.groups)-1]
	}
}

func (s *handleState) appendNonBuiltIns(r slog.Record) {
	// Attrs in Record -- unlike the built-in ones, they are in groups started
	// from WithGroup.
	// If the record has no Attrs, don't output any groups.
	if r.NumAttrs() > 0 {
		s.prefix.WriteString(s.h.groupPrefix)
		s.openGroups()
		r.Attrs(func(a slog.Attr) bool {
			s.appendAttr(a)
			return true
		})
	}
}

// appendAttr appends the Attr's key and value using app.
// It handles replacement and checking for an empty key.
// after replacement).
func (s *handleState) appendAttr(a slog.Attr) {
	if rep := s.h.opts.ReplaceAttr; rep != nil && a.Value.Kind() != slog.KindGroup {
		var gs []string
		if s.groups != nil {
			gs = *s.groups
		}
		// Resolve before calling ReplaceAttr, so the user doesn't have to.
		a.Value = a.Value.Resolve()
		a = rep(gs, a)
	}
	a.Value = a.Value.Resolve()

	// Elide empty Attrs.
	if a.Key == "" {
		return
	}

	// Special case: Source.
	if v := a.Value; v.Kind() == slog.KindAny {
		if src, ok := v.Any().(*slog.Source); ok {
			a.Value = slog.StringValue(fmt.Sprintf("%s:%d", src.File, src.Line))
		}
	}

	if a.Value.Kind() == slog.KindGroup {
		attrs := a.Value.Group()
		// Output only non-empty groups.
		if len(attrs) > 0 {
			// Inline a group with an empty key.
			if a.Key != "" {
				s.openGroup(a.Key)
			}
			for _, aa := range attrs {
				s.appendAttr(aa)
			}
			if a.Key != "" {
				s.closeGroup(a.Key)
			}
		}
	} else {
		s.appendKey(a.Key)
		s.appendValue(a.Value)
	}
}

func (s *handleState) appendKey(key string) {
	s.buf.WriteString(s.sep)
	if s.prefix != nil && len(*s.prefix) > 0 {
		// TODO: optimize by avoiding allocation.
		s.appendString(string(*s.prefix) + key)
	} else {
		s.appendString(key)
	}
	s.buf.WriteByte('=')
	s.sep = s.h.attrSep()
}

func (s *handleState) appendString(str string) {
	if needsQuoting(str) {
		*s.buf = strconv.AppendQuote(*s.buf, str)
	} else {
		s.buf.WriteString(str)
	}
}

func (s *handleState) appendValue(v slog.Value) {
	err := appendTextValue(s, v)
	if err != nil {
		s.appendError(err)
	}
}

func (s *handleState) appendTime(t time.Time) {
	year, month, day := t.UTC().Date()
	s.buf.WritePosIntWidth(year, 4)
	s.buf.WriteByte('-')
	s.buf.WritePosIntWidth(int(month), 2)
	s.buf.WriteByte('-')
	s.buf.WritePosIntWidth(day, 2)
	s.buf.WriteByte('T')
	hour, min, sec := t.UTC().Clock()
	s.buf.WritePosIntWidth(hour, 2)
	s.buf.WriteByte(':')
	s.buf.WritePosIntWidth(min, 2)
	s.buf.WriteByte(':')
	s.buf.WritePosIntWidth(sec, 2)
	ns := t.Nanosecond()
	s.buf.WriteByte('.')
	s.buf.WritePosIntWidth(ns/1e3, 3)
}

func (s *handleState) appendError(err error) {
	s.appendString(fmt.Sprintf("!ERROR:%v", err))
}

func (s *handleState) appendSep() {
	s.buf.WriteString(s.sep)
}

func appendTextValue(s *handleState, v slog.Value) error {
	switch v.Kind() {
	case slog.KindString:
		s.appendString(v.String())
	case slog.KindTime:
		s.appendTime(v.Time())
	case slog.KindAny:
		if tm, ok := v.Any().(encoding.TextMarshaler); ok {
			data, err := tm.MarshalText()
			if err != nil {
				return err
			}
			// TODO: avoid the conversion to string.
			s.appendString(string(data))
			return nil
		}
		if bs, ok := byteSlice(v.Any()); ok {
			// As of Go 1.19, this only allocates for strings longer than 32 bytes.
			s.buf.WriteString(strconv.Quote(string(bs)))
			return nil
		}
		s.appendString(fmt.Sprintf("%+v", v.Any()))
	case slog.KindInt64:
		*s.buf = strconv.AppendInt(*s.buf, v.Int64(), 10)
	case slog.KindUint64:
		*s.buf = strconv.AppendUint(*s.buf, v.Uint64(), 10)
	case slog.KindFloat64:
		*s.buf = strconv.AppendFloat(*s.buf, v.Float64(), 'g', -1, 64)
	case slog.KindBool:
		*s.buf = strconv.AppendBool(*s.buf, v.Bool())
	case slog.KindDuration:
		*s.buf = append(*s.buf, v.Duration().String()...)
	case slog.KindGroup:
		*s.buf = fmt.Append(*s.buf, v.Group())
	case slog.KindLogValuer:
		*s.buf = fmt.Append(*s.buf, v.Any())
	}
	return nil
}

// byteSlice returns its argument as a []byte if the argument's
// underlying type is []byte, along with a second return value of true.
// Otherwise it returns nil, false.
func byteSlice(a any) ([]byte, bool) {
	if bs, ok := a.([]byte); ok {
		return bs, true
	}
	// Like Printf's %s, we allow both the slice type and the byte element type to be named.
	t := reflect.TypeOf(a)
	if t != nil && t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
		return reflect.ValueOf(a).Bytes(), true
	}
	return nil, false
}

func needsQuoting(s string) bool {
	if len(s) == 0 {
		return true
	}
	for i := 0; i < len(s); {
		b := s[i]
		if b < utf8.RuneSelf {
			// Quote anything except a backslash that would need quoting in a
			// JSON string, as well as space and '='
			if b != '\\' && (b == ' ' || b == '=' || !safeSet[b]) {
				return true
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError || unicode.IsSpace(r) || !unicode.IsPrint(r) {
			return true
		}
		i += size
	}
	return false
}

// Copied from encoding/json/tables.go.
//
// safeSet holds the value true if the ASCII character with the given array
// position can be represented inside a JSON string without any further
// escaping.
//
// All values are true except for the ASCII control characters (0-31), the
// double quote ("), and the backslash character ("\").
var safeSet = [utf8.RuneSelf]bool{
	' ':      true,
	'!':      true,
	'"':      false,
	'#':      true,
	'$':      true,
	'%':      true,
	'&':      true,
	'\'':     true,
	'(':      true,
	')':      true,
	'*':      true,
	'+':      true,
	',':      true,
	'-':      true,
	'.':      true,
	'/':      true,
	'0':      true,
	'1':      true,
	'2':      true,
	'3':      true,
	'4':      true,
	'5':      true,
	'6':      true,
	'7':      true,
	'8':      true,
	'9':      true,
	':':      true,
	';':      true,
	'<':      true,
	'=':      true,
	'>':      true,
	'?':      true,
	'@':      true,
	'A':      true,
	'B':      true,
	'C':      true,
	'D':      true,
	'E':      true,
	'F':      true,
	'G':      true,
	'H':      true,
	'I':      true,
	'J':      true,
	'K':      true,
	'L':      true,
	'M':      true,
	'N':      true,
	'O':      true,
	'P':      true,
	'Q':      true,
	'R':      true,
	'S':      true,
	'T':      true,
	'U':      true,
	'V':      true,
	'W':      true,
	'X':      true,
	'Y':      true,
	'Z':      true,
	'[':      true,
	'\\':     false,
	']':      true,
	'^':      true,
	'_':      true,
	'`':      true,
	'a':      true,
	'b':      true,
	'c':      true,
	'd':      true,
	'e':      true,
	'f':      true,
	'g':      true,
	'h':      true,
	'i':      true,
	'j':      true,
	'k':      true,
	'l':      true,
	'm':      true,
	'n':      true,
	'o':      true,
	'p':      true,
	'q':      true,
	'r':      true,
	's':      true,
	't':      true,
	'u':      true,
	'v':      true,
	'w':      true,
	'x':      true,
	'y':      true,
	'z':      true,
	'{':      true,
	'|':      true,
	'}':      true,
	'~':      true,
	'\u007f': true,
}
