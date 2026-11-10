package playground

import (
	"testing"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// ⛔⛔ A TOOLBAR BUTTON CLICKED ONCE STAYED BLUE FOR THE REST OF THE SESSION.
//
// Reported from Chrome, and it is not CSS: these controls are painted, and a
// toolkit Button answers EventClick by setting its pressed face — which
// Button.Draw fills with theme.Accent (button.go:203-205) — until an
// EventMouseUp clears it. PressFeedback is on by default (:108), the toolkit
// documents that the host must route the pair ("EventMouseUp releases it.
// Self-managing the pressed state means any host that routes the press/release
// pair gets the …", :298), and HandleRelease had no case for pressToolbar at
// all, while four other regions — find, collab, git, sidebar — did route it.
//
// ⛔ ASSERTED ON THE PAINTED PIXEL, and the reason is a test of mine that was
// VACUOUS. The first version asked the widget through an interface assertion for
// `Pressed() bool` — a getter the toolkit does not have; it exposes SetPressed
// and no reader — so the assertion never matched, every control was skipped, and
// the test passed with the fix REMOVED. Only the break check caught it. The face
// is a colour on a surface, so the way to read it is to paint and look.
//
// ⛔ And it uses a control with NO sticky selection. sidebarBtn, findBtn and
// wysiwygBtn set Selected(), which Draw also fills with Accent *by design* — the
// sidebar is open, find is showing, WYSIWYG is on. Asserting "not Accent" on one
// of those would demand the removal of a state the app wants, so the subject is
// themeBtn: it cycles System/Light/Dark and owns no selection, so Accent on it
// can only be a stranded press.
func TestToolbarPressedFaceIsClearedOnRelease(t *testing.T) {
	s := newTestState(t, false)
	s.layout()

	btn := s.themeBtn
	r := btn.Bounds()
	if r.W <= 0 || r.H <= 0 {
		t.Fatalf("the theme button is not laid out: %v", r)
	}

	accent := s.theme.Accent
	face := func() painter.RGBA {
		// Paint the button alone into its own buffer and read the centre.
		buf := make([]byte, r.W*r.H*4)
		p := painter.NewPixelPainter(buf, r.W, r.H)
		b := btn
		saved := b.Bounds()
		b.SetBounds(toolkit.Rect{X: 0, Y: 0, W: r.W, H: r.H})
		b.Draw(p, s.theme)
		b.SetBounds(saved)
		// ⛔ NOT the centre: that is where the LABEL is, and a pressed button draws
		// its ink in theme.Background — so sampling the middle reads the text
		// colour, near-white, and reports "not Accent" however pressed it is.
		// Measured that way first, and it made the test fail on the press rather
		// than on the release. A few pixels in from the left edge, past the
		// 1-pixel border, is face.
		i := ((r.H/2)*r.W + 3) * 4
		return painter.RGBA{R: buf[i], G: buf[i+1], B: buf[i+2], A: buf[i+3]}
	}

	resting := face()
	if sameRGBA(resting, accent) {
		t.Fatalf("the theme button is already Accent at rest (%v) — this test "+
			"cannot tell a stranded press from the resting face", resting)
	}

	x, y := r.X+r.W/2, r.Y+r.H/2
	if !s.HandleClick(x, y) {
		t.Fatal("the theme button did not take the press")
	}
	if pressedFace := face(); !sameRGBA(pressedFace, accent) {
		t.Fatalf("after the press the face is %v, want Accent %v — if the toolkit "+
			"stopped showing press feedback, this test is measuring nothing",
			pressedFace, accent)
	}

	s.HandleRelease(x, y)
	if after := face(); sameRGBA(after, accent) {
		t.Error("the theme button kept its Accent face after the release — " +
			"this is the bug: clicked once, blue for ever")
	}
}

func sameRGBA(a, b painter.RGBA) bool {
	return a.R == b.R && a.G == b.G && a.B == b.B && a.A == b.A
}
