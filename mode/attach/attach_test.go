package attach

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/proto"
)

// pngBytes returns a small valid PNG.
func pngBytes(t testing.TB) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// imageAtt wraps image bytes as an inline attachment.
func imageAtt(data []byte) proto.Attachment {
	return proto.Attachment{Kind: "image", Name: "shot.png", MimeType: "image/png", Data: base64.StdEncoding.EncodeToString(data)}
}

func TestMessage_NoAttachmentsIsPlainText(t *testing.T) {
	msg, err := Message("hello", nil, false)
	if err != nil || msg.Content != "hello" || msg.HasImages() {
		t.Fatalf("Message = %+v, %v", msg, err)
	}
}

func TestMessage_TextAttachmentJoinsTheContent(t *testing.T) {
	msg, err := Message("see", []proto.Attachment{{Kind: "text", Text: "notes"}}, false)
	if err != nil || msg.Content != "see\n\nnotes" {
		t.Fatalf("content = %q, %v", msg.Content, err)
	}
}

func TestMessage_ImageNeedsAModelThatTakesImages(t *testing.T) {
	atts := []proto.Attachment{imageAtt(pngBytes(t))}
	msg, err := Message("look", atts, true)
	if err != nil || !msg.HasImages() {
		t.Fatalf("Message = %+v, %v; want an image part", msg, err)
	}
	if _, err := Message("look", atts, false); err == nil || !strings.Contains(err.Error(), "does not support images") {
		t.Fatalf("text-only model error = %v", err)
	}
}

func TestMessage_KindFollowsTheMimeTypeWhenOmitted(t *testing.T) {
	att := imageAtt(pngBytes(t))
	att.Kind = ""
	if msg, err := Message("", []proto.Attachment{att}, true); err != nil || !msg.HasImages() {
		t.Fatalf("Message = %+v, %v", msg, err)
	}
}

func TestMessage_RefusesBadAttachments(t *testing.T) {
	good := imageAtt(pngBytes(t))
	cases := map[string]proto.Attachment{
		"a server path": {Kind: "image", Name: "x", Path: "/etc/passwd"},
		"no data":       {Kind: "image", Name: "x"},
		"not base64":    {Kind: "image", Name: "x", Data: "!!!not base64!!!"},
		"not an image":  imageAtt([]byte("plain text, not a picture")),
		"unknown kind":  {Kind: "audio", Data: good.Data},
		"too large":     {Kind: "image", Name: "x", Data: strings.Repeat("A", base64.StdEncoding.EncodedLen(MaxBytes)+8)},
	}
	for name, att := range cases {
		if _, err := Message("x", []proto.Attachment{att}, true); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// The count limit is checked before an image is normalized, so a prompt of many
// undecodable images costs one count error, not one decode attempt each.
func TestMessage_ImageCountLimit(t *testing.T) {
	good := imageAtt(pngBytes(t))
	atts := make([]proto.Attachment, 0, MaxImages+1)
	for range MaxImages {
		atts = append(atts, good)
	}
	if _, err := Message("x", atts, true); err != nil {
		t.Fatalf("%d images: %v", MaxImages, err)
	}
	atts = append(atts, imageAtt([]byte("garbage that would fail to decode")))
	_, err := Message("x", atts, true)
	if err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("one image too many: error = %v, want the count limit", err)
	}
}

func TestMessage_TotalSizeLimit(t *testing.T) {
	// Bytes after the PNG's end are ignored by the decoder, so padding makes a
	// valid image that is large without being expensive to decode.
	padded := append(pngBytes(t), make([]byte, MaxBytes/2+1)...)
	atts := []proto.Attachment{imageAtt(padded), imageAtt(padded)}
	_, err := Message("x", atts, true)
	if err == nil || !strings.Contains(err.Error(), "total at most") {
		t.Fatalf("error = %v, want the total size limit", err)
	}
}

func TestAccepts(t *testing.T) {
	if !Accepts([]string{"text", "image"}) || Accepts([]string{"text"}) || Accepts(nil) {
		t.Fatal("Accepts disagrees with the modality list")
	}
}

// FuzzMessage feeds arbitrary attachment fields, which come straight from a
// client. It must return a value or an error, never panic or hang.
func FuzzMessage(f *testing.F) {
	f.Add("image", "image/png", "shot.png", base64.StdEncoding.EncodeToString(pngBytes(f)), "", "", true)
	f.Add("text", "", "", "", "", "note", false)
	f.Add("", "image/jpeg", "x", "====", "/etc/passwd", "", true)
	f.Fuzz(func(t *testing.T, kind, mime, name, data, path, text string, accepts bool) {
		_, _ = Message("prompt", []proto.Attachment{{Kind: kind, MimeType: mime, Name: name, Data: data, Path: path, Text: text}}, accepts)
	})
}
