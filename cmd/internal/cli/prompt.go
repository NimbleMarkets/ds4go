package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/internal/cliopts"
	"github.com/NimbleMarkets/ds4go/internal/models"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type cliMessage struct {
	role    string
	content string
	// parts, when set, is the multimodal content (text and images) and
	// replaces content for prompt rendering.
	parts []ds4.ContentPart
}

// isImageFile reports whether data starts with a PNG or JPEG signature.
func isImageFile(data []byte) bool {
	return bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) || bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff})
}

// readInputMessage turns a /read file into the next user message: images
// become an image part after a short caption, anything else is the text.
// The image part carries the bytes rather than the path: chat history is
// re-rendered every turn, and the file may be deleted or overwritten by then.
func readInputMessage(path string, data []byte) cliMessage {
	if isImageFile(data) {
		return cliMessage{role: "user", parts: []ds4.ContentPart{
			{Text: "[image: " + filepath.Base(path) + "]\n"},
			{Image: &ds4.ImageInput{Data: data}},
		}}
	}
	return cliMessage{role: "user", content: string(data)}
}

// visionHint is the one-line remedy when an image is used without an encoder.
func visionHint(cfg *cliopts.CLIConfig) string {
	hint := "start with --vision ENCODER.gguf"
	if model, ok := models.ModelForPath(cfg.Model); ok && model.Encoder != "" {
		hint += " (or: ds4go model download " + model.Encoder + ", which pairs automatically)"
	}
	return hint
}

func newPromptCommand() *cobra.Command {
	fs := pflag.NewFlagSet("prompt", pflag.ContinueOnError)
	cfg := cliopts.RegisterCLI(fs)
	cmd := &cobra.Command{
		Use:   "prompt [options]",
		Short: "Run prompt or interactive chat inference",
		Long:  "Run ds4 inference. With no prompt, starts an interactive chat (ds4>).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			return run(cfg)
		},
	}
	cmd.Flags().AddFlagSet(fs)
	return cmd
}

func run(cfg *cliopts.CLIConfig) error {
	if err := validatePromptFlags(cfg); err != nil {
		return err
	}
	cfg.Model = modelManager().ResolvePath(cfg.Model)
	if err := preflightPromptModel(cfg.Model); err != nil {
		return err
	}
	var engine *ds4.Engine
	var err error
	if cfg.Lib != "" {
		lib, err := ds4.Load(cfg.Lib)
		if err != nil {
			return err
		}
		ds4.SetDefaultLibrary(lib)
		engine, err = lib.NewEngine(cfg.EngineOptions())
	} else {
		engine, err = ds4.NewEngine(cfg.EngineOptions())
	}
	if err != nil {
		return ds4.EnrichEngineOpenError(err)
	}
	defer engine.Close()

	// --inspect and imatrix collection run without a session timeline.
	switch {
	case cfg.Inspect:
		if err := engine.Summary(); err != nil {
			return err
		}
		if name := engine.ModelName(); name != "" {
			fmt.Printf("Model: %s (id=%d)\n", name, engine.ModelID())
		}
		if engine.HasVision() {
			fmt.Println("Vision encoder: loaded")
		} else {
			fmt.Println("Vision encoder: none")
		}
		return nil
	case cfg.IMatrixOut != "":
		return collectIMatrix(engine, cfg)
	case cfg.PerplexityFile != "":
		return runPerplexity(engine, cfg, os.Stdout)
	}

	if diag := diagnostic(cfg); diag != "" {
		return runDiagnostic(engine, cfg, diag)
	}

	session, err := engine.NewSession(cfg.Ctx)
	if err != nil {
		return err
	}
	defer session.Close()

	promptText, err := cfg.PromptText()
	if err != nil {
		return err
	}
	if cfg.DumpLogits != "" || cfg.DecodeConsistency > 0 {
		prompt, err := encodePrompt(engine, cfg, promptText)
		if err != nil {
			return err
		}
		defer prompt.Free()
		if cfg.DumpLogits != "" {
			return dumpLogits(engine, session, cfg, prompt)
		}
		return runDecodeConsistency(engine, cfg, prompt, os.Stderr)
	}
	if cfg.DumpLogprobs != "" {
		return dumpLogprobs(engine, session, cfg, promptText)
	}
	if promptText != "" {
		return generateOne(engine, session, cfg, promptText)
	}
	return chat(engine, session, cfg)
}

func preflightPromptModel(path string) error {
	if path == "" {
		return fmt.Errorf("no model path configured; run: ds4go model download %s", models.RecommendedModelAlias)
	}
	st, err := os.Stat(path)
	if err == nil && !st.IsDir() && st.Size() > 0 {
		return nil
	}
	if err == nil && st.IsDir() {
		return fmt.Errorf("model path is a directory: %s", path)
	}

	defaultPath := models.DefaultModelPath()
	if path == defaultPath {
		m := modelManager()
		list, cfg, listErr := m.List()
		if listErr != nil {
			return fmt.Errorf("model is not ready at %s; additionally failed to inspect model config: %w", path, listErr)
		}
		if cfg.DefaultModel == "" || activeDefault(list) == "" {
			return fmt.Errorf("no default model is installed at %s\nRun: ds4go model download %s\nOr:  ds4go model list", path, models.RecommendedModelAlias)
		}
		return fmt.Errorf("configured default model %q is not available at %s\nRun: ds4go model download %s\nOr:  ds4go model set <installed-alias>", cfg.DefaultModel, path, cfg.DefaultModel)
	}
	if _, ok := models.Lookup(path); ok {
		// A catalog alias that is not installed yet.
		return fmt.Errorf("model %s is not installed\nRun: ds4go model download %s", path, path)
	}
	return fmt.Errorf("model file not found: %s\nUse --model PATH or run: ds4go model download %s", path, models.RecommendedModelAlias)
}

// diagnostic returns the name of the selected one-shot diagnostic, if any.
func diagnostic(cfg *cliopts.CLIConfig) string {
	switch {
	case cfg.DumpTokens:
		return "dump-tokens"
	case cfg.HeadTest:
		return "head-test"
	case cfg.FirstTokenTest:
		return "first-token-test"
	case cfg.MetalGraphTest:
		return "metal-graph-test"
	case cfg.MetalGraphFullTest:
		return "metal-graph-full-test"
	case cfg.MetalGraphPromptTest:
		return "metal-graph-prompt-test"
	default:
		return ""
	}
}

func runDiagnostic(engine *ds4.Engine, cfg *cliopts.CLIConfig, diag string) error {
	promptText, err := cfg.PromptText()
	if err != nil {
		return err
	}
	prompt, err := encodePrompt(engine, cfg, promptText)
	if err != nil {
		return err
	}
	defer prompt.Free()

	switch diag {
	case "dump-tokens":
		return engine.DumpTokens(prompt)
	case "head-test":
		return engine.HeadTest(prompt)
	case "first-token-test":
		return engine.FirstTokenTest(prompt)
	case "metal-graph-test":
		return engine.MetalGraphTest(prompt)
	case "metal-graph-full-test":
		return engine.MetalGraphFullTest(prompt)
	case "metal-graph-prompt-test":
		return engine.MetalGraphPromptTest(prompt, cfg.Ctx)
	default:
		return fmt.Errorf("unknown diagnostic %q", diag)
	}
}

func generateOne(engine *ds4.Engine, session *ds4.Session, cfg *cliopts.CLIConfig, promptText string) error {
	opts := cfg.GenerateOptions()
	opts.OnToken = func(token int) {
		if text, err := engine.TokenText(token); err == nil {
			fmt.Print(text)
		}
	}

	if parts := cfg.ImageParts(promptText); parts != nil {
		msgs := []ds4.ChatMessage{{Role: "user", Parts: parts}}
		prompt, err := ds4.BuildChatPromptMultimodal(engine, ds4.NewImageEncoder(engine), cfg.System, nil, msgs, cfg.ThinkMode())
		if err != nil {
			if ds4.IsVisionEncoderMissing(err) {
				fmt.Fprintln(os.Stderr, "ds4: this prompt has an image but no vision encoder is loaded; "+visionHint(cfg))
			}
			return err
		}
		defer prompt.Free()
		_, err = (ds4.Generator{Engine: engine, Session: session}).GeneratePrompt(prompt, opts)
		fmt.Println()
		if err != nil && ds4.IsVisionEncoderMissing(err) {
			fmt.Fprintln(os.Stderr, "ds4: this prompt has an image but no vision encoder is loaded; "+visionHint(cfg))
		}
		return err
	}

	tokens, err := encodePrompt(engine, cfg, promptText)
	if err != nil {
		return err
	}
	defer tokens.Free()

	_, err = (ds4.Generator{Engine: engine, Session: session}).GenerateTokens(tokens, opts)
	fmt.Println()
	return err
}

func chat(engine *ds4.Engine, session *ds4.Session, cfg *cliopts.CLIConfig) error {
	// A --prefix-file seeds the conversation, as upstream's REPL does.
	history, err := prefixHistory(cfg)
	if err != nil {
		return err
	}
	in := bufio.NewScanner(os.Stdin)
	thinkMode := cfg.ThinkMode()
	ctxSize := cfg.Ctx
	images := ds4.NewImageEncoder(engine)

	// Mirror --inspect's wording so it is clear up front whether /read can
	// attach an image this session.
	if engine.HasVision() {
		fmt.Println("Vision encoder: loaded")
	} else {
		fmt.Println("Vision encoder: none")
	}

	defer func() {
		if session != nil {
			session.Close()
		}
	}()

	for {
		fmt.Print("ds4> ")
		if !in.Scan() {
			return in.Err()
		}
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "/") {
			parts := strings.Fields(line)
			cmd := parts[0]
			switch cmd {
			case "/quit", "/exit":
				return nil
			case "/help":
				fmt.Println("Commands:")
				fmt.Println("  /help          Show this help.")
				fmt.Println("  /think [N]     Use normal thinking, or V4.1 effort 0..100 (0 disables thinking).")
				fmt.Println("  /think-max     Use Think Max only when context is at least 393216 tokens.")
				fmt.Println("  /nothink       Disable thinking mode.")
				fmt.Println("  /ctx N         Set context size for following prompts.")
				fmt.Println("  /power N       Set GPU duty cycle percentage, 1..100.")
				fmt.Println("  /read FILE     Send FILE as the next turn: a PNG or JPEG as an image, anything else as text.")
				fmt.Println("                 A PNG or JPEG is attached as an image instead (needs a vision encoder).")
				fmt.Println("  /quit, /exit   Leave the prompt.")
				continue

			case "/think":
				arg := ""
				if len(parts) > 1 {
					arg = parts[1]
				}
				mode, err := parseThinkCommand(engine, arg)
				if err != nil {
					fmt.Fprintln(os.Stderr, "ds4: "+err.Error())
					continue
				}
				thinkMode = mode
				if level := mode.Level(); level >= 0 {
					fmt.Printf("Thinking mode: %d.\n", level)
				} else {
					fmt.Println("Thinking mode: high.")
				}
				continue

			case "/think-max":
				thinkMode = ds4.ThinkMax
				active := ds4api.ThinkModeForContext(thinkMode, ctxSize) == ds4.ThinkMax
				if active {
					fmt.Println("Thinking mode: max.")
				} else {
					fmt.Println("Thinking mode: high (ctx below 393216). Warning: think-max was downgraded because --ctx is below 393216.")
				}
				continue

			case "/nothink":
				thinkMode = ds4.ThinkNone
				fmt.Println("Thinking mode: none.")
				continue

			case "/power":
				if len(parts) < 2 {
					fmt.Printf("Power: %d%%.\n", session.Power())
				} else {
					power, err := strconv.Atoi(parts[1])
					if err != nil || power < 1 || power > 100 {
						fmt.Fprintln(os.Stderr, "ds4: /power must be between 1 and 100")
					} else {
						if err := session.SetPower(power); err != nil {
							fmt.Fprintf(os.Stderr, "ds4: failed to set /power: %v\n", err)
						} else {
							fmt.Printf("Power: %d%%.\n", power)
						}
					}
				}
				continue

			case "/ctx":
				if len(parts) < 2 {
					fmt.Fprintln(os.Stderr, "ds4: /ctx needs a positive integer")
				} else {
					newCtx, err := strconv.Atoi(parts[1])
					if err != nil || newCtx <= 0 {
						fmt.Fprintln(os.Stderr, "ds4: /ctx needs a positive integer")
					} else {
						ctxSize = newCtx
						session.Close()
						session = nil
						newSess, err := engine.NewSession(ctxSize)
						if err != nil {
							return fmt.Errorf("ds4: failed to create new session: %w", err)
						}
						session = newSess
						history = nil
						fmt.Printf("Context size set to %d. Chat history reset.\n", ctxSize)
					}
				}
				continue

			case "/read":
				if len(parts) < 2 {
					fmt.Fprintln(os.Stderr, "ds4: /read needs a file path")
				} else {
					filePath := parts[1]
					content, err := os.ReadFile(filePath)
					if err != nil {
						fmt.Fprintf(os.Stderr, "ds4: failed to read %s: %v\n", filePath, err)
					} else {
						fmt.Printf("[Reading prompt from %s...]\n", filePath)
						err := runChatTurn(engine, session, cfg, images, &history, readInputMessage(filePath, content), thinkMode)
						if err != nil {
							fmt.Fprintf(os.Stderr, "Generation error: %v\n", err)
						}
					}
				}
				continue

			default:
				fmt.Fprintf(os.Stderr, "ds4: unknown option: %s\n", cmd)
				continue
			}
		}

		err := runChatTurn(engine, session, cfg, images, &history, cliMessage{role: "user", content: line}, thinkMode)
		if err != nil {
			return err
		}
	}
}

func runChatTurn(engine *ds4.Engine, session *ds4.Session, cfg *cliopts.CLIConfig, images *ds4.ImageEncoder, history *[]cliMessage, msg cliMessage, thinkMode ds4.ThinkMode) error {
	*history = append(*history, msg)
	prompt, err := buildChatPrompt(engine, images, cfg.System, *history, thinkMode)
	if err != nil {
		*history = (*history)[:len(*history)-1]
		if ds4.IsVisionEncoderMissing(err) {
			fmt.Fprintln(os.Stderr, "ds4: this prompt has an image but no vision encoder is loaded; "+visionHint(cfg))
		}
		return err
	}
	defer prompt.Free()

	opts := cfg.GenerateOptions()
	// The REPL's /think commands change the mode per turn, so the prompt's
	// mode wins over the one baked in from the flags.
	opts.ThinkMode = thinkMode
	var response strings.Builder
	opts.OnToken = func(token int) {
		if text, err := engine.TokenText(token); err == nil {
			response.WriteString(text)
			fmt.Print(text)
		}
	}
	_, err = (ds4.Generator{Engine: engine, Session: session}).GeneratePrompt(prompt, opts)
	fmt.Println()
	if err != nil {
		*history = (*history)[:len(*history)-1]
		if ds4.IsVisionEncoderMissing(err) {
			fmt.Fprintln(os.Stderr, "ds4: this prompt has an image but no vision encoder is loaded; "+visionHint(cfg))
		}
		return err
	}
	*history = append(*history, cliMessage{role: "assistant", content: response.String()})
	return nil
}

func buildChatPrompt(engine *ds4.Engine, images *ds4.ImageEncoder, system string, history []cliMessage, think ds4.ThinkMode) (*ds4.Prompt, error) {
	msgs := make([]ds4.ChatMessage, 0, len(history))
	for _, m := range history {
		msgs = append(msgs, ds4.ChatMessage{Role: m.role, Content: m.content, Parts: m.parts})
	}
	return ds4.BuildChatPromptMultimodal(engine, images, system, nil, msgs, think)
}

// logprobStep is one greedy generation step recorded by --dump-logprobs.
type logprobStep struct {
	Token int            `json:"token"`
	Text  string         `json:"text"`
	Top   []logprobScore `json:"top"`
}

type logprobScore struct {
	ID      int     `json:"id"`
	Logit   float32 `json:"logit"`
	Logprob float32 `json:"logprob"`
}

func dumpLogprobs(engine *ds4.Engine, session *ds4.Session, cfg *cliopts.CLIConfig, promptText string) error {
	tokens, err := encodePrompt(engine, cfg, promptText)
	if err != nil {
		return err
	}
	defer tokens.Free()
	if err := session.SyncTokens(tokens); err != nil {
		return err
	}

	eos := engine.TokenEOS()
	steps := make([]logprobStep, 0, cfg.Tokens)
	for i := 0; i < cfg.Tokens; i++ {
		top, err := session.TopLogprobs(cfg.LogprobsTopK)
		if err != nil {
			return err
		}
		token := session.Argmax()
		if token == eos {
			break
		}
		text, _ := engine.TokenText(token)
		scores := make([]logprobScore, len(top))
		for j, s := range top {
			scores[j] = logprobScore{ID: s.ID, Logit: s.Logit, Logprob: s.Logprob}
		}
		steps = append(steps, logprobStep{Token: token, Text: text, Top: scores})
		if err := session.Eval(token); err != nil {
			return err
		}
	}

	f, err := os.Create(cfg.DumpLogprobs)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(steps)
}
