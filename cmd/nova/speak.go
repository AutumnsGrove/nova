package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/AutumnsGrove/nova/mode"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/voice"
)

// speakCommand synthesizes speech from arguments or stdin and writes audio.
func speakCommand(stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	var (
		model, voiceID, role, format, instructions, out string
		speed                                           float64
		chunkSize                                       int
		noChunk, asJSON                                 bool
	)
	cmd := &cobra.Command{
		Use:   "speak [text]",
		Short: "Synthesize speech and write audio",
		Long: "Synthesize speech from text, or from stdin when no text is given. Long input is split at\n" +
			"sentence boundaries and joined, because a model's own input cap is far below Nova's.",
		RunE: func(cmd *cobra.Command, args []string) error {
			text, err := commandText(stdin, args)
			if err != nil {
				return err
			}
			if asJSON && out == "-" {
				return errors.New("speak: --json and -o - both write to stdout; name a file with -o")
			}
			v, err := mode.NewVoice(mode.BuildOptions{Mode: "speak", Stderr: stderr})
			if err != nil {
				return err
			}
			chosen, err := pickVoiceModel(model, role, v.SpeechRoles(), "speech")
			if err != nil {
				return err
			}
			req := voice.SpeechRequest{
				Model: chosen, Input: text, Voice: voiceID,
				Format: voice.Format(format), Instructions: instructions,
			}
			if speed != 0 {
				req.Speed = &speed
			}
			chunks := []string{strings.TrimSpace(text)}
			if !noChunk {
				chunks = voice.Chunks(text, chunkSize)
			}
			clips := make([]voice.Clip, 0, len(chunks))
			for i, chunk := range chunks {
				attempt := req
				attempt.Input = chunk
				clip, err := v.Speak(cmd.Context(), attempt)
				if err != nil {
					// Writing nothing on a partial answer is the point: a file
					// that looks complete but is not is worse than no file.
					return fmt.Errorf("speak: chunk %d of %d: %w", i+1, len(chunks), err)
				}
				clips = append(clips, *clip)
			}
			clip, err := voice.Join(clips...)
			if err != nil {
				return fmt.Errorf("speak: %w", err)
			}
			audio := clip.Bytes
			if clip.Format == voice.FormatPCM {
				if audio = voice.WAV(clip); audio == nil {
					return errors.New("speak: the pcm clip has no sample layout, so it cannot be written as a wav")
				}
			}
			written := out
			if out == "-" {
				if isCharDevice(stdout) {
					return errors.New("speak: refusing to write audio to the terminal; use -o FILE, or -o - when the output is piped")
				}
				if _, err := stdout.Write(audio); err != nil {
					return fmt.Errorf("speak: writing output: %w", err)
				}
			} else if err := os.WriteFile(out, audio, 0o644); err != nil {
				return fmt.Errorf("speak: writing %s: %w", out, err)
			}
			if asJSON {
				return writeJSON(stdout, newSpeakSummary(clip, written, len(chunks), len(audio)))
			}
			if out != "-" {
				fmt.Fprintf(stderr, "wrote %s: %d bytes, %d chunk(s)\n", out, len(audio), len(chunks))
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&model, "model", "", "model id (wins over --role)")
	f.StringVar(&role, "role", "", "configured voice role to use")
	f.StringVar(&voiceID, "voice", "", "voice id the model accepts")
	f.StringVar(&format, "format", "", "audio format: mp3 or pcm (default mp3)")
	f.Float64Var(&speed, "speed", 0, "playback multiplier")
	f.StringVar(&instructions, "instructions", "", "delivery notes, where the model supports them")
	f.StringVarP(&out, "output", "o", "-", "write the audio here, or - for stdout")
	f.IntVar(&chunkSize, "chunk-size", voice.ChunkRunes, "maximum runes per synthesis call")
	f.BoolVar(&noChunk, "no-chunk", false, "send the whole text in one call instead of chunking")
	f.BoolVar(&asJSON, "json", false, "print the clip's metadata as JSON")
	return cmd
}

// transcribeCommand turns one audio file into text.
func transcribeCommand(stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	var (
		model, role, language, format, timestamps string
		temperature                               float64
		verbose, asJSON                           bool
	)
	cmd := &cobra.Command{
		Use:   "transcribe <file>",
		Short: "Transcribe audio to text",
		Long:  "Transcribe one audio file, or stdin when the path is -. The format comes from the extension unless --format says otherwise.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			var (
				audio []byte
				err   error
			)
			if path == "-" {
				audio, err = io.ReadAll(stdin)
				if err != nil {
					return fmt.Errorf("transcribe: reading stdin: %w", err)
				}
			} else {
				audio, err = os.ReadFile(path)
				if err != nil {
					return fmt.Errorf("transcribe: %w", err)
				}
			}
			audioFormat := voice.Format(format)
			if audioFormat == "" {
				audioFormat = formatFromPath(path)
			}
			if audioFormat == "" {
				return errors.New("transcribe: cannot tell the audio format; pass --format")
			}
			v, err := mode.NewVoice(mode.BuildOptions{Mode: "transcribe", Stderr: stderr})
			if err != nil {
				return err
			}
			chosen, err := pickVoiceModel(model, role, v.TranscribeRoles(), "transcription")
			if err != nil {
				return err
			}
			req := voice.TranscribeRequest{
				Model: chosen, Audio: audio, Format: audioFormat,
				Language: language, Verbose: verbose,
			}
			if temperature != 0 {
				req.Temperature = &temperature
			}
			if timestamps != "" {
				req.Timestamps = splitList(timestamps)
				req.Verbose = true
			}
			transcript, err := v.Transcribe(cmd.Context(), req)
			if err != nil {
				return fmt.Errorf("transcribe: %w", err)
			}
			if asJSON {
				return writeJSON(stdout, transcript)
			}
			fmt.Fprintln(stdout, transcript.Text)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&model, "model", "", "model id (wins over --role)")
	f.StringVar(&role, "role", "", "configured transcription role to use")
	f.StringVar(&language, "language", "", "ISO-639-1 language; empty auto-detects")
	f.StringVar(&format, "format", "", "audio format: wav, mp3, flac, m4a, ogg, webm, aac")
	f.Float64Var(&temperature, "temperature", 0, "sampling temperature, 0 to 1")
	f.StringVar(&timestamps, "timestamps", "", "comma-separated granularities: segment, word")
	f.BoolVar(&verbose, "verbose", false, "ask for verbose_json, which carries duration and language")
	f.BoolVar(&asJSON, "json", false, "print the whole transcript as JSON")
	return cmd
}

// commandText joins the arguments, or reads stdin when there are none.
func commandText(stdin io.Reader, args []string) (string, error) {
	text := strings.Join(args, " ")
	if text == "" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("speak: reading stdin: %w", err)
		}
		text = string(data)
	}
	if strings.TrimSpace(text) == "" {
		return "", errors.New("speak: no text; pass it as an argument or on stdin")
	}
	// No total-length check here: one chunk is validated before its own request,
	// so chunking lets an input longer than the guard succeed while --no-chunk
	// refuses it with the same message the library gives.
	return text, nil
}

// pickVoiceModel resolves --model, then --role, then a configured "default"
// role, and explains the configured roles when none of those exist.
func pickVoiceModel(model, role string, roles []string, kind string) (string, error) {
	if model != "" {
		return model, nil
	}
	if role != "" {
		return role, nil
	}
	for _, name := range roles {
		if name == "default" {
			return "default", nil
		}
	}
	if len(roles) == 0 {
		return "", fmt.Errorf("no %s model: pass --model, or add one under voice.%s.roles in the config", kind, kind)
	}
	return "", fmt.Errorf("no %s model: pass --model or --role (configured roles: %s)", kind, strings.Join(roles, ", "))
}

// formatFromPath infers an audio format from a file extension.
func formatFromPath(path string) voice.Format {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3":
		return voice.FormatMP3
	case ".wav":
		return voice.FormatWAV
	case ".flac":
		return voice.FormatFLAC
	case ".m4a":
		return voice.FormatM4A
	case ".ogg", ".oga", ".opus":
		return voice.FormatOGG
	case ".webm":
		return voice.FormatWebM
	case ".aac":
		return voice.FormatAAC
	}
	return ""
}

// speakSummary is the --json shape: metadata and a byte count, never the audio.
type speakSummary struct {
	File   string         `json:"file"`
	Bytes  int            `json:"bytes"`
	Chunks int            `json:"chunks"`
	Format voice.Format   `json:"format"`
	MIME   string         `json:"mime"`
	Model  string         `json:"model"`
	Voice  string         `json:"voice"`
	Usage  provider.Usage `json:"usage"`
}

// newSpeakSummary builds the JSON view of a joined clip.
func newSpeakSummary(clip voice.Clip, file string, chunks, size int) speakSummary {
	if file == "-" {
		file = ""
	}
	return speakSummary{
		File: file, Bytes: size, Chunks: chunks,
		Format: clip.Format, MIME: clip.MIME, Model: clip.Model, Voice: clip.Voice,
		Usage: clip.Usage,
	}
}

// writeJSON writes one indented JSON value.
func writeJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

// isCharDevice reports whether w is a character device, which is how a terminal
// is recognised without a dependency. /dev/null is one too, so a caller that
// redirects there should name a file with -o instead.
func isCharDevice(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
