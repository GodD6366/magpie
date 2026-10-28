package agent

// OpenClaw (openclaw/openclaw, a personal assistant that answers on
// WhatsApp, Telegram, Discord and the rest) keeps its settings in one file,
// openclaw.json. Where it is follows OpenClaw's own resolveConfigPath
// (src/config/paths.ts) and resolveStateDir (src/config/state-dir.ts):
// $OPENCLAW_CONFIG_PATH, else openclaw.json in $OPENCLAW_STATE_DIR, else in
// ~/.openclaw-<profile> for a named $OPENCLAW_PROFILE, else in ~/.openclaw,
// where ~ is $OPENCLAW_HOME when it is set.
//
// A provider of the user's own is an entry of models.providers
// (src/config/zod-schema.core.ts, ModelProviderSchema): a baseUrl, an
// apiKey, an api (openai-completions is OpenAI's chat completions) and its
// models, each an id and a name, with input, reasoning and contextWindow
// (ModelDefinitionSchema). The schema is strict: a key OpenClaw doesn't
// know makes the file invalid, so magpie writes only those. A model is
// named provider/model, split at the first slash
// (src/agents/model-selection-normalize.ts), so magpie's
// magpie/<provider>/<model> is OpenClaw's ref as it is. The model OpenClaw
// runs on is agents.defaults.model, a ref or {primary, fallbacks}
// (docs/concepts/models.md); `openclaw models set` sets primary and keeps
// the fallbacks, and adds the ref to agents.defaults.models
// (src/commands/models/shared.ts), which a config from before 2026.8.1
// still reads as the models allowed. A non-empty
// agents.defaults.modelPolicy.allow is the models allowed: magpie adds
// "magpie/*" to it when nothing of magpie's is in it.
//
// magpie adds itself as the provider "magpie", the gateway's /v1 with a key
// naming OpenClaw and one model entry per catalog model, and makes the model
// picked the primary. What was the primary is stashed and put back on
// Disconnect, while magpie's is still the primary. The user's own keys stay
// as written, in magpie's provider and its model entries too (headers,
// timeoutSeconds, a model's maxTokens). OpenClaw writes the file as JSON
// (src/config/io.write.ts) but reads JSON5; a file that isn't JSON magpie
// can read is left alone. OpenClaw's gateway takes a change to models and
// the default model without a restart (docs/gateway/configuration/
// hot-reload.md), but a chat already pinned to a model keeps it until
// /model default.
//
// The GUI also edits the whole sequence: ReadModelChain and SetModelChain
// read and write agents.defaults.model's primary and fallbacks together,
// with the agent's own model override (agents.entries.*.model or
// agents.list[].model) kept in step, as OpenClaw resolves that first. A
// chain may start on a model of OpenClaw's own and reach magpie only on
// fallback, which keeps magpie's provider in the file. The values written
// are OpenClaw's refs as they are (magpie/<provider>/<model> is magpie's).
//
// Not wired: a config split with $include.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/jsonc"

	"github.com/yetone/magpie/internal/edit"
)

// what OpenClaw says after a change (notice.go)
var noticeOpenClaw = newNotice("OpenClaw takes this up without a restart. A chat that already has a model keeps it: send /model default in that chat to move it onto this one.")

// openClawContext is what OpenClaw is told of a model whose context magpie
// doesn't know.
const openClawContext = 128000

// the key paths magpie writes in openclaw.json
const (
	openClawProvider = "models.providers." + magpieID
	openClawModel    = "agents.defaults.model"
	openClawModels   = "agents.defaults.models"
	openClawAllow    = "agents.defaults.modelPolicy.allow"
	openClawAll      = magpieID + "/*"
)

// openClawProfile is a profile name OpenClaw takes (profile-utils.ts).
var openClawProfile = regexp.MustCompile(`(?i)^[a-z0-9][a-z0-9_-]{0,63}$`)

// openClawHome is OpenClaw's ~: $OPENCLAW_HOME, a leading ~ in it the
// system's home (resolveEffectiveHomeDir).
func openClawHome(at place) string {
	h := strings.TrimSpace(at.getenv("OPENCLAW_HOME"))
	if h == "" || h == "undefined" || h == "null" {
		return at.home
	}
	return openClawTilde(h, at.home)
}

// openClawTilde is p with a leading ~ as home.
func openClawTilde(p, home string) string {
	if p == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(home, rest)
	}
	if rest, ok := strings.CutPrefix(p, `~\`); ok {
		return filepath.Join(home, rest)
	}
	return p
}

// openClawPaths are OpenClaw's state folder and its openclaw.json.
func openClawPaths(at place) (dir, path string) {
	home := openClawHome(at)
	dir = filepath.Join(home, ".openclaw")
	if p := strings.TrimSpace(at.getenv("OPENCLAW_PROFILE")); p != "" && !strings.EqualFold(p, "default") && openClawProfile.MatchString(p) {
		dir = filepath.Join(home, ".openclaw-"+p)
	}
	if d := strings.TrimSpace(at.getenv("OPENCLAW_STATE_DIR")); d != "" {
		dir = openClawTilde(d, home)
	}
	path = filepath.Join(dir, "openclaw.json")
	if p := strings.TrimSpace(at.getenv("OPENCLAW_CONFIG_PATH")); p != "" {
		path = openClawTilde(p, home)
	}
	return dir, path
}

// openClawRead is openclaw.json as JSON with its comments blanked out, nil
// for a missing file; one that isn't JSON (JSON5's bare keys) is an error,
// never taken as empty.
func openClawRead(path string) ([]byte, error) {
	b, err := edit.Read(path)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil, nil
	}
	s := jsonc.ToJSONInPlace(append([]byte(nil), b...))
	if !json.Valid(s) || !gjson.ParseBytes(s).IsObject() {
		return nil, fmt.Errorf("OpenClaw's %s isn't JSON magpie can read (JSON5?); it was left as it is", path)
	}
	return s, nil
}

// openClawKey is a key as one part of a key path.
func openClawKey(k string) string { return strings.ReplaceAll(k, ".", `\.`) }

// openClawEntry is one of magpie's models as OpenClaw is told it.
type openClawEntry struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Reasoning     bool     `json:"reasoning"`
	Input         []string `json:"input"`
	ContextWindow int      `json:"contextWindow"`
}

// openClawWant is magpie's models as OpenClaw is told them, each laid over
// the entry of the same id there was (raw), so the user's own keys in it
// stay.
func openClawWant(raw gjson.Result) []any {
	was := map[string]map[string]any{}
	for _, m := range raw.Array() {
		var e map[string]any
		if json.Unmarshal([]byte(m.Raw), &e) == nil {
			if id, _ := e["id"].(string); id != "" {
				was[id] = e
			}
		}
	}
	out := []any{}
	for _, m := range magpieModels("openclaw") {
		ctx := m.Context
		if ctx <= 0 {
			ctx = openClawContext
		}
		e := openClawEntry{ID: m.ID, Name: m.ID, Input: []string{"text"}, ContextWindow: ctx}
		if m.Images {
			e.Input = append(e.Input, "image")
		}
		e.Reasoning = slices.ContainsFunc(m.Efforts, func(l string) bool { return l != "none" })
		var mine map[string]any
		b, _ := json.Marshal(e)
		json.Unmarshal(b, &mine)
		kept := was[m.ID]
		if kept == nil {
			out = append(out, mine)
			continue
		}
		for k, v := range mine {
			kept[k] = v
		}
		out = append(out, kept)
	}
	return out
}

// openClawPrimary is the model a value of agents.defaults.model names: the
// string, or an object's primary.
func openClawPrimary(r gjson.Result) string {
	if r.IsObject() {
		return r.Get("primary").String()
	}
	if r.Type == gjson.String {
		return r.String()
	}
	return ""
}

// openClawWas is what agents.defaults.model was before magpie, as written,
// which of the objects holding magpie's keys magpie made, and the sequences
// magpie wrote there (model, model menu entries, allow entries).
type openClawWas struct {
	Had   bool            `json:"had,omitempty"`
	Model json.RawMessage `json:"model,omitempty"`
	// Wrote is agents.defaults.model as magpie last left it
	Wrote json.RawMessage `json:"wrote,omitempty"`
	// Made are the key paths of objects that weren't there before magpie
	Made []string `json:"made,omitempty"`
	// Menu and Allow are the entries magpie added to agents.defaults.models
	// and agents.defaults.modelPolicy.allow, as full key paths and values
	Menu  []string `json:"menu,omitempty"`
	Allow []string `json:"allow,omitempty"`
	// OverrideKey, Override and WroteOverride: the selected agent's own model
	// (agents.entries.*.model or agents.list[].model) as it was, and as
	// magpie left it
	OverrideKey   string          `json:"overrideKey,omitempty"`
	Override      json.RawMessage `json:"override,omitempty"`
	WroteOverride json.RawMessage `json:"wroteOverride,omitempty"`
	// File: the file wasn't there before magpie
	File bool `json:"file,omitempty"`
}

// openClawProviderAt is magpie's provider entry for a gateway at gw, as
// it is written (models aside).
func openClawProviderAt(gw string) map[string]string {
	return map[string]string{"baseUrl": gw + "/v1", "apiKey": agentKeyAt("openclaw", gw), "api": "openai-completions"}
}

func openClaw(home string) *Agent { return openClawAt(here(home)) }

// openclawIn is the agent at a place (wslKind.in).
func openclawIn(at place) *Agent { return openClawAt(at) }

func openClawAt(at place) *Agent {
	dir, path := openClawPaths(at)
	// A relative key also works on the copied HOME used by disconnect
	// preview (preview.go).
	rel, err := filepath.Rel(at.home, path)
	if err != nil {
		rel = path // another volume on Windows
	}
	keyWas := at.key("openclaw.config:") + filepath.ToSlash(rel) + ":model"
	read := func() gjson.Result {
		s, _ := openClawRead(path)
		return gjson.ParseBytes(s)
	}
	ours := func(c gjson.Result) bool { return prefixed(openClawPrimary(c.Get(openClawModel))) }
	writable := func() error {
		if at.getenv("OPENCLAW_CONFIG_READONLY") == "1" || at.getenv("OPENCLAW_NIX_MODE") == "1" {
			return fmt.Errorf("OpenClaw config is read-only")
		}
		return nil
	}
	// patch applies sets and deletes to the file as it is, in one write
	patch := func(set []edit.KV, del []string) error {
		if err := writable(); err != nil {
			return err
		}
		raw, err := edit.Read(path)
		if err != nil {
			return err
		}
		out, err := edit.PatchJSON(raw, set, del)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if raw == nil {
			// OpenClaw keeps its folder to its user (io.write.ts), and the
			// file holds keys
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				return err
			}
		}
		return edit.WriteAtomic(path, out)
	}
	// wire writes magpie's provider and its models, the user's own keys in
	// it left as they are; with chain, also the default model's primary and
	// (a non-nil Fallbacks) its fallbacks, the model menu and the allow
	// list. was, if given, records what magpie added there.
	wire := func(c gjson.Result, chain *ModelChain, was *openClawWas) ([]edit.KV, []string) {
		var set []edit.KV
		var del []string
		p := c.Get(openClawProvider)
		for k, v := range openClawProviderAt(at.gw()) {
			if p.Get(k).String() != v || p.Get(k).Type != gjson.String {
				set = append(set, edit.KV{Path: openClawProvider + "." + k, Value: v})
			}
		}
		want := openClawWant(p.Get("models"))
		if !sameJSON(p.Get("models").Raw, want) {
			set = append(set, edit.KV{Path: openClawProvider + ".models", Value: want})
		}
		if chain == nil {
			return set, del
		}
		ref := chain.Primary
		refs := append([]string{ref}, chain.Fallbacks...)
		m := c.Get(openClawModel)
		if m.IsObject() {
			if openClawPrimary(m) != ref {
				set = append(set, edit.KV{Path: openClawModel + ".primary", Value: ref})
			}
			if chain.Fallbacks != nil && !sameJSON(m.Get("fallbacks").Raw, chain.Fallbacks) {
				set = append(set, edit.KV{Path: openClawModel + ".fallbacks", Value: chain.Fallbacks})
			}
		} else if len(chain.Fallbacks) > 0 {
			set = append(set, edit.KV{Path: openClawModel, Value: map[string]any{
				"primary": ref, "fallbacks": chain.Fallbacks,
			}})
		} else if m.String() != ref || m.Type != gjson.String {
			set = append(set, edit.KV{Path: openClawModel, Value: ref})
		}
		// as `openclaw models set` does; the refs of models magpie put there
		// before go, as they had nothing of the user's in them
		if ms := c.Get(openClawModels); ms.IsObject() {
			ms.ForEach(func(k, v gjson.Result) bool {
				if prefixed(k.String()) && !slices.Contains(refs, k.String()) && v.IsObject() && len(v.Map()) == 0 {
					del = append(del, openClawModels+"."+openClawKey(k.String()))
				}
				return true
			})
			for _, r := range refs {
				if r == "" {
					continue
				}
				k := openClawModels + "." + openClawKey(r)
				if !c.Get(k).Exists() {
					set = append(set, edit.KV{Path: k, Value: map[string]any{}})
					if was != nil {
						was.Menu = append(was.Menu, k)
					}
				}
			}
		}
		// a restricted list must admit every ref magpie puts there as well
		if allow := c.Get(openClawAllow); allow.IsArray() && len(allow.Array()) > 0 {
			covers := func(r string) bool {
				return slices.ContainsFunc(allow.Array(), func(a gjson.Result) bool {
					s := a.String()
					if s == "*" || s == r || s == openClawAll {
						return true
					}
					if prefixed(r) {
						return s == magpieID+"/"+strings.SplitN(strings.TrimPrefix(r, magpieID+"/"), "/", 2)[0]+"/*"
					}
					return s == strings.SplitN(r, "/", 2)[0]+"/*"
				})
			}
			var add []any
			magpie := false
			for _, r := range refs {
				if r == "" || covers(r) || slices.ContainsFunc(add, func(v any) bool { return v == r }) {
					continue
				}
				if prefixed(r) {
					magpie = true
					continue
				}
				add = append(add, r)
			}
			if magpie && !covers(openClawAll) {
				add = append(add, openClawAll)
			}
			if len(add) > 0 {
				var list []any
				json.Unmarshal([]byte(allow.Raw), &list)
				set = append(set, edit.KV{Path: openClawAllow, Value: append(list, add...)})
				if was != nil {
					for _, v := range add {
						was.Allow = append(was.Allow, v.(string))
					}
				}
			}
		}
		return set, del
	}
	// unwire takes magpie's provider out, and OpenClaw back to the model it
	// ran on before — when it still runs on magpie's: a pick the user made
	// since is theirs and stands. own, if given, is the model to run on.
	unwire := func(own string) error {
		s, err := openClawRead(path)
		if err != nil {
			return err
		}
		c := gjson.ParseBytes(s)
		var was openClawWas
		json.Unmarshal([]byte(stashLoad()[keyWas]), &was)
		var set []edit.KV
		del := []string{openClawProvider}
		m := c.Get(openClawModel)
		orig := gjson.ParseBytes(was.Model)
		switch {
		case own != "":
			if m.IsObject() {
				set = append(set, edit.KV{Path: openClawModel + ".primary", Value: own})
			} else {
				set = append(set, edit.KV{Path: openClawModel, Value: own})
			}
		case len(was.Wrote) > 0 && sameJSON(m.Raw, json.RawMessage(was.Wrote)):
			// magpie's sequence as it left it: the value goes back
			if !was.Had {
				del = append(del, openClawModel)
			} else if orig.IsObject() && m.IsObject() {
				// only what magpie changed, so the rest keeps its layout
				if !sameOpenClawValue(m.Get("primary"), orig.Get("primary")) {
					if p := orig.Get("primary"); p.Exists() {
						set = append(set, edit.KV{Path: openClawModel + ".primary", Value: p.Value()})
					} else {
						del = append(del, openClawModel+".primary")
					}
				}
				if !sameOpenClawValue(m.Get("fallbacks"), orig.Get("fallbacks")) {
					if f := orig.Get("fallbacks"); f.Exists() {
						set = append(set, edit.KV{Path: openClawModel + ".fallbacks", Value: f.Value()})
					} else {
						del = append(del, openClawModel+".fallbacks")
					}
				}
			} else {
				set = append(set, edit.KV{Path: openClawModel, Value: was.Model})
			}
		case !ours(c) && !openClawChainMine(c):
			// the user's own pick stands
		default:
			// the user changed part of it: magpie's own values go back
			if ours(c) {
				if p := openClawPrimary(orig); p != "" {
					if m.IsObject() {
						set = append(set, edit.KV{Path: openClawModel + ".primary", Value: p})
					} else {
						set = append(set, edit.KV{Path: openClawModel, Value: p})
					}
				} else if m.IsObject() && len(m.Map()) > 1 {
					del = append(del, openClawModel+".primary")
				} else {
					del = append(del, openClawModel)
				}
			}
			if m.IsObject() && openClawFallbacksMine(m) {
				if f := orig.Get("fallbacks"); f.Exists() {
					set = append(set, edit.KV{Path: openClawModel + ".fallbacks", Value: f.Value()})
				} else {
					del = append(del, openClawModel+".fallbacks")
				}
			}
			// A string original stays a string where only magpie's keys are
			// left in the object magpie made of it.
			if orig.Type == gjson.String && m.IsObject() && !slices.ContainsFunc(set, func(kv edit.KV) bool {
				return strings.HasPrefix(kv.Path, openClawModel+".")
			}) {
				set = append(set, edit.KV{Path: openClawModel, Value: was.Model})
			}
		}
		if ms := c.Get(openClawModels); ms.IsObject() {
			ms.ForEach(func(k, v gjson.Result) bool {
				if v.IsObject() && len(v.Map()) == 0 && (prefixed(k.String()) || slices.Contains(was.Menu, openClawModels+"."+openClawKey(k.String()))) {
					del = append(del, openClawModels+"."+openClawKey(k.String()))
				}
				return true
			})
		}
		if allow := c.Get(openClawAllow); allow.IsArray() && len(was.Allow) > 0 {
			var list []any
			for _, a := range allow.Array() {
				if !slices.Contains(was.Allow, a.String()) {
					list = append(list, a.Value())
				}
			}
			if len(list) < len(allow.Array()) {
				if list == nil {
					list = []any{}
				}
				set = append(set, edit.KV{Path: openClawAllow, Value: list})
			}
		}
		// the selected agent's own model magpie kept in step goes back too
		if was.OverrideKey != "" {
			cur := c.Get(was.OverrideKey)
			if len(was.WroteOverride) > 0 && sameJSON(cur.Raw, json.RawMessage(was.WroteOverride)) || prefixed(openClawPrimary(cur)) {
				if len(was.Override) > 0 {
					set = append(set, edit.KV{Path: was.OverrideKey, Value: was.Override})
				} else {
					del = append(del, was.OverrideKey)
				}
			}
		}
		if err := patch(set, del); err != nil {
			return err
		}
		// the objects magpie made, where nothing else is in them now,
		// deepest first; and the file, where magpie made it and it is
		// left empty
		for i := len(was.Made) - 1; i >= 0; i-- {
			k := was.Made[i]
			if strings.HasSuffix(k, "[]") {
				continue
			}
			if r := read().Get(k); r.IsObject() && len(r.Map()) == 0 {
				if err := edit.DelJSON(path, k); err != nil {
					return err
				}
			}
		}
		if was.File {
			if r := read(); r.IsObject() && len(r.Map()) == 0 {
				return os.Remove(path)
			}
		}
		return nil
	}
	// apply makes chain the default model (and the agent's own override, where
	// there is one), stashing what was there first
	apply := func(chain ModelChain) error {
		if err := writable(); err != nil {
			return err
		}
		if err := validateOpenClawChain(chain); err != nil {
			return err
		}
		s, err := openClawRead(path)
		if err != nil {
			return err
		}
		c := gjson.ParseBytes(s)
		var was openClawWas
		if b := stashLoad()[keyWas]; b != "" {
			json.Unmarshal([]byte(b), &was)
		} else if !ours(c) {
			m := c.Get(openClawModel)
			was = openClawWas{Had: m.Exists(), File: s == nil}
			if m.Exists() {
				was.Model = json.RawMessage(m.Raw)
			}
		}
		for _, k := range []string{"models", "models.providers", "agents", "agents.defaults"} {
			if !c.Get(k).Exists() && !slices.Contains(was.Made, k) {
				was.Made = append(was.Made, k)
			}
		}
		set, del := wire(c, &chain, &was)
		// The selected agent's model overrides defaults. Keep that override in
		// sync without changing models belonging to other agents.
		if target := openClawAgentModel(c); target != "" {
			v := c.Get(target)
			if v.Exists() {
				if was.OverrideKey != target {
					was.OverrideKey = target
					was.Override = json.RawMessage(v.Raw)
				}
				value := map[string]any{}
				if v.IsObject() {
					if err := json.Unmarshal([]byte(v.Raw), &value); err != nil {
						return err
					}
				} else if v.Type == gjson.String {
					value["primary"] = v.String()
				}
				value["primary"] = chain.Primary
				if chain.Fallbacks != nil {
					value["fallbacks"] = chain.Fallbacks
				}
				set = append(set, edit.KV{Path: target, Value: value})
			}
		}
		if err := patch(set, del); err != nil {
			return err
		}
		// what magpie left in agents.defaults.model and the override, for
		// unwire
		last := read()
		if r := last.Get(openClawModel); r.Exists() {
			was.Wrote = json.RawMessage(r.Raw)
		}
		if was.OverrideKey != "" {
			if r := last.Get(was.OverrideKey); r.Exists() {
				was.WroteOverride = json.RawMessage(r.Raw)
			}
		}
		b, _ := json.Marshal(was)
		stash(map[string]string{keyWas: string(b)})
		return nil
	}
	connect := func(ref string) error { return apply(ModelChain{Primary: ref}) }
	return atomic(&Agent{
		ID: "openclaw", Name: "OpenClaw", Icon: "openclaw-color", Spelled: prefixed, NoNames: true,
		Bin: "openclaw", Dir: dir, Path: path,
		Notice: func() string { return noticeOpenClaw.String() },
		Unwire: func() error { return unwire("") },
		Joined: func() bool {
			s, err := openClawRead(path)
			if err != nil {
				return false
			}
			return gatewayKey(gjson.ParseBytes(s).Get(openClawProvider + ".apiKey").String())
		},
		ReadModelChain: func() (ModelChain, error) {
			s, err := openClawRead(path)
			if err != nil {
				return ModelChain{}, err
			}
			return openClawChain(gjson.ParseBytes(s))
		},
		SetModelChain: func(chain ModelChain) error {
			if chain.Fallbacks == nil {
				chain.Fallbacks = []string{}
			}
			return apply(chain)
		},
		Sync: func() error {
			s, err := openClawRead(path)
			if err != nil {
				return err
			}
			c := gjson.ParseBytes(s)
			if !c.Get(openClawProvider).Exists() {
				return nil
			}
			set, del := wire(c, nil, nil)
			if len(set) == 0 && len(del) == 0 {
				return nil
			}
			return patch(set, del)
		},
		Check: func() string {
			s, err := openClawRead(path)
			if err != nil {
				return err.Error()
			}
			c := gjson.ParseBytes(s)
			if !ours(c) && !openClawChainMine(c) {
				return ""
			}
			p := c.Get(openClawProvider)
			if !p.Exists() {
				return "OpenClaw's " + openClawProvider + " (openclaw.json) is gone, so it no longer reaches magpie"
			}
			want := openClawProviderAt(at.gw())
			return wiringOff("OpenClaw", path, func(k string) (string, bool) {
				v := p.Get(k)
				return v.String(), v.Exists()
			}, "baseUrl", want["baseUrl"], "apiKey", want["apiKey"])
		},
		Fields: []Field{{
			// the model OpenClaw runs on: one of magpie's is
			// magpie/<provider>/<model>, OpenClaw's ref for it as it is
			Key: "model", Label: "model",
			Get: func() string { return openClawPrimary(read().Get(openClawModel)) },
			Set: func(v string) error {
				s, err := openClawRead(path)
				if err != nil {
					return err
				}
				c := gjson.ParseBytes(s)
				if ref, ok := strings.CutPrefix(v, magpieID+"/"); ok && isMagpie(ref) {
					return connect(v)
				}
				if c.Get(openClawProvider).Exists() {
					// a chain that reaches magpie on fallback keeps the
					// provider: only the model it starts on changes
					if openClawFallbacksMine(c.Get(openClawModel)) && v != "" {
						return patch([]edit.KV{{Path: openClawModel + ".primary", Value: v}}, nil)
					}
					return unwire(v)
				}
				if ours(c) {
					return unwire(v)
				}
				if v == "" {
					return nil
				}
				if m := c.Get(openClawModel); m.IsObject() {
					return patch([]edit.KV{{Path: openClawModel + ".primary", Value: v}}, nil)
				}
				return patch([]edit.KV{{Path: openClawModel, Value: v}}, nil)
			},
			Options: func(cur map[string]string) []Option {
				return append(openClawOwn(keyWas, cur["model"]), viaMagpie("openclaw", magpieID+"/")...)
			},
		}},
	}, path)
}

// openClawOwn is the model OpenClaw ran on before magpie, or runs on now,
// for the picker: OpenClaw's own providers are its sign-ins and plugins,
// which magpie can't list.
func openClawOwn(keyWas, cur string) []Option {
	var was openClawWas
	json.Unmarshal([]byte(stashLoad()[keyWas]), &was)
	var out []Option
	for _, m := range []string{openClawPrimary(gjson.ParseBytes(was.Model)), cur} {
		if m == "" || prefixed(m) || slices.ContainsFunc(out, func(o Option) bool { return o.Value == m }) {
			continue
		}
		p, id, _ := strings.Cut(m, "/")
		out = append(out, Option{Value: m, Icon: modelIcon(p, id), Note: "OpenClaw's own"})
	}
	return group("openclaw", out)
}

// openClawChain is the effective sequence: the selected agent's override,
// where it has one, else agents.defaults.model.
func openClawChain(c gjson.Result) (ModelChain, error) {
	chain, err := openClawParseChain(c.Get(openClawModel))
	if err != nil {
		return chain, err
	}
	if target := openClawAgentModel(c); target != "" {
		v := c.Get(target)
		own, err := openClawParseChain(v)
		if err != nil {
			return chain, err
		}
		if own.Primary != "" {
			chain = own
		} else if v.Get("fallbacks").Exists() {
			chain.Fallbacks = own.Fallbacks
		}
	}
	return chain, nil
}

// Resolve only a configured default or sole agent. An explicit fleet without
// an owner continues to edit global defaults rather than guessing an agent.
func openClawAgentModel(c gjson.Result) string {
	type entry struct {
		id, path string
		marked   bool
	}
	var entries []entry
	roster := c.Get("agents.entries")
	if roster.Exists() {
		roster.ForEach(func(k, v gjson.Result) bool {
			if v.IsObject() {
				entries = append(entries, entry{k.String(), "agents.entries." + openClawKey(k.String()) + ".model", v.Get("default").Bool()})
			}
			return true
		})
	} else {
		for i, v := range c.Get("agents.list").Array() {
			if v.IsObject() {
				entries = append(entries, entry{v.Get("id").String(), fmt.Sprintf("agents.list.%d.model", i), v.Get("default").Bool()})
			}
		}
	}
	owner := c.Get("agents.defaults.systemAgent.agentId").String()
	for _, e := range entries {
		if owner != "" && e.id == owner {
			return e.path
		}
	}
	if c.Get("agents.ownership").String() != "explicit" {
		var marked []entry
		for _, e := range entries {
			if e.marked {
				marked = append(marked, e)
			}
		}
		if len(marked) == 1 {
			return marked[0].path
		}
	}
	if len(entries) == 1 {
		return entries[0].path
	}
	return ""
}

func openClawParseChain(v gjson.Result) (ModelChain, error) {
	c := ModelChain{Fallbacks: []string{}}
	if v.Type == gjson.String {
		c.Primary = v.String()
		return c, nil
	}
	if v.Exists() && !v.IsObject() {
		return c, fmt.Errorf("OpenClaw model must be a string or an object")
	}
	c.Primary = v.Get("primary").String()
	if f := v.Get("fallbacks"); f.Exists() {
		if !f.IsArray() {
			return c, fmt.Errorf("OpenClaw fallbacks must be an array of model IDs")
		}
		if err := json.Unmarshal([]byte(f.Raw), &c.Fallbacks); err != nil {
			return c, err
		}
	}
	return c, nil
}

func validateOpenClawChain(c ModelChain) error {
	seen := map[string]bool{}
	for _, id := range append([]string{c.Primary}, c.Fallbacks...) {
		provider, model, ok := strings.Cut(id, "/")
		if !ok || provider == "" || model == "" || strings.ContainsAny(id, " \t\r\n") {
			return fmt.Errorf("OpenClaw model %q must be provider/model", id)
		}
		if seen[id] {
			return fmt.Errorf("OpenClaw model %q is repeated", id)
		}
		seen[id] = true
	}
	return nil
}

// openClawFallbacksMine says an agents.defaults.model value has a fallback
// of magpie's, so magpie's provider must stay while it is set.
// sameOpenClawValue reports whether two gjson values say the same, absent
// ones alike.
func sameOpenClawValue(a, b gjson.Result) bool {
	if !a.Exists() || !b.Exists() {
		return a.Exists() == b.Exists()
	}
	return sameJSON(a.Raw, json.RawMessage(b.Raw))
}

func openClawFallbacksMine(m gjson.Result) bool {
	return slices.ContainsFunc(m.Get("fallbacks").Array(), func(f gjson.Result) bool {
		return prefixed(f.String())
	})
}

// openClawChainMine says the value is connected to magpie: a magpie model,
// or a fallback of magpie's.
func openClawChainMine(c gjson.Result) bool {
	chain, err := openClawChain(c)
	if err != nil {
		return false
	}
	return prefixed(chain.Primary) || slices.ContainsFunc(chain.Fallbacks, prefixed)
}
