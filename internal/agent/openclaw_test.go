package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/provider"
)

// openClawConfig is openclaw.json as OpenClaw 2026.10.8 writes it after
// `openclaw onboard --non-interactive --accept-risk --auth-choice
// deepseek-api-key …` (the folder and the gateway token changed).
const openClawConfig = `{
  "agents": {
    "entries": {
      "main": {
        "name": "main",
        "workspace": "/Users/u/.openclaw/workspace",
        "agentDir": "/Users/u/.openclaw/agents/main/agent",
        "identity": {
          "name": "main"
        }
      }
    },
    "defaults": {
      "workspace": "/Users/u/.openclaw/workspace",
      "models": {
        "deepseek/deepseek-v4-pro": {
          "alias": "DeepSeek"
        }
      },
      "model": {
        "primary": "deepseek/deepseek-v4-pro"
      }
    }
  },
  "plugins": {
    "entries": {
      "anthropic": {
        "config": {
          "sessionCatalog": {
            "enabled": false
          }
        }
      },
      "codex": {
        "config": {
          "sessionCatalog": {
            "enabled": false
          }
        }
      },
      "deepseek": {
        "enabled": true
      }
    }
  },
  "meta": {
    "migrations": {
      "modelPolicyAllowlist": true,
      "utilityModelSeparation": true
    },
    "lastTouchedVersion": "2026.10.8"
  },
  "gateway": {
    "mode": "local",
    "auth": {
      "mode": "token",
      "token": "gateway-token"
    },
    "port": 18799,
    "bind": "loopback",
    "tailscale": {
      "mode": "off"
    }
  },
  "tools": {
    "profile": "full"
  },
  "auth": {
    "profiles": {
      "deepseek:default": {
        "provider": "deepseek",
        "mode": "api_key"
      }
    }
  },
  "models": {
    "mode": "merge",
    "providers": {
      "deepseek": {
        "baseUrl": "https://api.deepseek.com",
        "api": "openai-completions",
        "models": []
      }
    }
  },
  "wizard": {
    "lastRunAt": "2026-10-28T20:43:15.671Z",
    "lastRunVersion": "2026.10.8",
    "lastRunCommand": "onboard",
    "lastRunMode": "local"
  }
}
`

// openClawHomeWith is a HOME with OpenClaw's ~/.openclaw holding body as
// its openclaw.json ("" for none), and magpie with a provider of two
// models, one of them with effort levels.
func openClawHomeWith(t *testing.T, body string) (home, path string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	for _, k := range []string{"OPENCLAW_CONFIG_PATH", "OPENCLAW_STATE_DIR", "OPENCLAW_PROFILE", "OPENCLAW_HOME"} {
		t.Setenv(k, "")
	}
	if err := provider.Save(provider.Provider{ID: "kimi", Name: "Kimi", Key: "k",
		Chat: "https://api.moonshot.cn/v1", Models: []string{"k2", "k2.5-vision"}}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveLive("kimi", "https://api.moonshot.cn/v1", []catalog.Model{
		{ID: "k2", Context: 262144, Efforts: []string{"low", "medium", "high"}},
		{ID: "k2.5-vision", Images: true},
	}); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(home, ".openclaw", "openclaw.json")
	if body != "" {
		writeFile(t, path, body)
	}
	return home, path
}

func openClawParse(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s is not JSON: %v\n%s", path, err, b)
	}
	return m
}

func openClawAt_(m map[string]any, keys ...string) any {
	var v any = m
	for _, k := range keys {
		o, _ := v.(map[string]any)
		v = o[k]
	}
	return v
}

// Connecting OpenClaw adds magpie as a provider of its own — the gateway,
// with a key naming OpenClaw, and every one of magpie's models — and makes
// the model picked the primary. The rest of OpenClaw's file stays, and
// Disconnect gives it back byte for byte.
func TestOpenClaw(t *testing.T) {
	home, path := openClawHomeWith(t, openClawConfig)
	a := openClaw(home)
	if a.Path != path || !a.Detected() {
		t.Fatalf("path %q, detected %v", a.Path, a.Detected())
	}
	f := a.Field("model")
	if got := f.Get(); got != "deepseek/deepseek-v4-pro" || a.Wired() || a.Check() != "" {
		t.Fatalf("before: %q wired %v check %q", got, a.Wired(), a.Check())
	}
	var own, ours bool
	for _, o := range f.Options(a.Values()) {
		own = own || o.Value == "deepseek/deepseek-v4-pro"
		ours = ours || o.Value == "magpie/kimi/k2"
	}
	if !own || !ours {
		t.Fatalf("options: own %v magpie %v", own, ours)
	}

	if err := a.Pick("model", "magpie/kimi/k2"); err != nil {
		t.Fatal(err)
	}
	m := openClawParse(t, path)
	if p := openClawAt_(m, "agents", "defaults", "model"); !reflect.DeepEqual(p, map[string]any{"primary": "magpie/kimi/k2"}) {
		t.Fatalf("agents.defaults.model: %v", p)
	}
	if !a.Wired() || f.Get() != "magpie/kimi/k2" {
		t.Fatalf("wired %v, model %q", a.Wired(), f.Get())
	}
	if c := a.Check(); c != "" {
		t.Fatalf("check: %s", c)
	}
	p, _ := openClawAt_(m, "models", "providers", "magpie").(map[string]any)
	if p["baseUrl"] != here(home).v1() || p["api"] != "openai-completions" || p["apiKey"] != agentKeyAt("openclaw", here(home).gw()) {
		t.Fatalf("magpie's provider: %v", p)
	}
	models := map[string]map[string]any{}
	for _, x := range p["models"].([]any) {
		e := x.(map[string]any)
		models[e["id"].(string)] = e
	}
	k2, vision := models["kimi/k2"], models["kimi/k2.5-vision"]
	if len(models) != 2 || k2 == nil || vision == nil {
		t.Fatalf("models: %v", models)
	}
	if k2["name"] != "kimi/k2" || k2["contextWindow"] != float64(262144) || k2["reasoning"] != true {
		t.Fatalf("k2: %v", k2)
	}
	if vision["contextWindow"] != float64(openClawContext) || !reflect.DeepEqual(vision["input"], []any{"text", "image"}) || vision["reasoning"] != false {
		t.Fatalf("k2.5-vision: %v", vision)
	}
	// as `openclaw models set` does, beside the user's own entry
	if ms := openClawAt_(m, "agents", "defaults", "models"); !reflect.DeepEqual(ms, map[string]any{
		"deepseek/deepseek-v4-pro": map[string]any{"alias": "DeepSeek"}, "magpie/kimi/k2": map[string]any{}}) {
		t.Fatalf("agents.defaults.models: %v", ms)
	}
	for _, k := range []string{"plugins", "meta", "gateway", "tools", "auth", "wizard"} {
		var want map[string]any
		json.Unmarshal([]byte(openClawConfig), &want)
		if !reflect.DeepEqual(m[k], want[k]) {
			t.Fatalf("%s changed: %v", k, m[k])
		}
	}
	if d := openClawAt_(m, "models", "providers", "deepseek"); d == nil {
		t.Fatal("the user's own provider is gone")
	}

	// switching between magpie's models keeps what OpenClaw was on before;
	// a model ref with a dot is one key
	if err := a.Pick("model", "magpie/kimi/k2.5-vision"); err != nil {
		t.Fatal(err)
	}
	if got := f.Get(); got != "magpie/kimi/k2.5-vision" {
		t.Fatalf("model %q", got)
	}
	ms, _ := openClawAt_(openClawParse(t, path), "agents", "defaults", "models").(map[string]any)
	if _, on := ms["magpie/kimi/k2.5-vision"]; !on || len(ms) != 2 {
		t.Fatalf("agents.defaults.models after a switch: %v", ms)
	}
	if err := a.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != openClawConfig {
		t.Fatalf("Disconnect left:\n%s", got)
	}
	if a.Wired() {
		t.Fatal("still wired")
	}
}

// A model OpenClaw names as a bare string stays a string, and one with no
// model at all, or no file at all, is given back as it was.
func TestOpenClawShapes(t *testing.T) {
	for name, body := range map[string]string{
		"string":   "{\n  \"agents\": {\n    \"defaults\": {\n      \"model\": \"anthropic/claude-opus-4-6\"\n    }\n  }\n}\n",
		"fallback": "{\n  \"agents\": {\n    \"defaults\": {\n      \"model\": {\n        \"primary\": \"anthropic/claude-opus-4-6\",\n        \"fallbacks\": [\"openai/gpt-5.5\"]\n      }\n    }\n  }\n}\n",
		"no model": "{\n  \"gateway\": {\n    \"port\": 18789\n  }\n}\n",
		"no file":  "",
	} {
		t.Run(name, func(t *testing.T) {
			home, path := openClawHomeWith(t, body)
			if body == "" {
				os.MkdirAll(filepath.Dir(path), 0o700) // detected
			}
			a := openClaw(home)
			if err := a.Pick("model", "magpie/kimi/k2"); err != nil {
				t.Fatal(err)
			}
			m := openClawParse(t, path)
			got := openClawAt_(m, "agents", "defaults", "model")
			switch name {
			case "fallback":
				if !reflect.DeepEqual(got, map[string]any{"primary": "magpie/kimi/k2", "fallbacks": []any{"openai/gpt-5.5"}}) {
					t.Fatalf("model: %v", got)
				}
			default:
				if got != "magpie/kimi/k2" {
					t.Fatalf("model: %v", got)
				}
			}
			if body == "" && runtime.GOOS != "windows" {
				if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
					t.Fatalf("a new openclaw.json holding a key: %v %v", st.Mode(), err)
				}
			}
			if err := a.Disconnect(); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(path)
			if body == "" {
				if !os.IsNotExist(err) {
					t.Fatalf("magpie's own file is left: %v\n%s", err, b)
				}
				return
			}
			if string(b) != body {
				t.Fatalf("Disconnect left:\n%s\nwant:\n%s", b, body)
			}
		})
	}
}

// A model allow-list that has nothing of magpie's gets magpie's models
// in, or OpenClaw refuses them; Disconnect takes them out again.
func TestOpenClawAllowList(t *testing.T) {
	body := "{\n  \"agents\": {\n    \"defaults\": {\n      \"model\": \"deepseek/deepseek-v4-pro\",\n      \"modelPolicy\": {\n        \"allow\": [\n          \"deepseek/*\"\n        ]\n      }\n    }\n  }\n}\n"
	home, path := openClawHomeWith(t, body)
	a := openClaw(home)
	if err := a.Pick("model", "magpie/kimi/k2"); err != nil {
		t.Fatal(err)
	}
	allow := openClawAt_(openClawParse(t, path), "agents", "defaults", "modelPolicy", "allow")
	if !reflect.DeepEqual(allow, []any{"deepseek/*", "magpie/*"}) {
		t.Fatalf("allow: %v", allow)
	}
	if err := a.Disconnect(); err != nil {
		t.Fatal(err)
	}
	m := openClawParse(t, path)
	if allow := openClawAt_(m, "agents", "defaults", "modelPolicy", "allow"); !reflect.DeepEqual(allow, []any{"deepseek/*"}) {
		t.Fatalf("allow after Disconnect: %v", allow)
	}
	if got := openClawAt_(m, "agents", "defaults", "model"); got != "deepseek/deepseek-v4-pro" {
		t.Fatalf("model after Disconnect: %v", got)
	}
}

// A model of OpenClaw's own picked while it is on magpie takes magpie
// out, and keeps the fallbacks.
func TestOpenClawOwnModelTakesMagpieOut(t *testing.T) {
	home, path := openClawHomeWith(t, openClawConfig)
	a := openClaw(home)
	if err := a.Pick("model", "magpie/kimi/k2"); err != nil {
		t.Fatal(err)
	}
	if err := a.Pick("model", "deepseek/deepseek-v4-flash"); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(openClawConfig, `"primary": "deepseek/deepseek-v4-pro"`, `"primary": "deepseek/deepseek-v4-flash"`, 1)
	if got, _ := os.ReadFile(path); string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if a.Wired() {
		t.Fatal("still wired")
	}
}

// What the user sets in magpie's provider — a header, a timeout, a
// model's maxTokens — is theirs: a catalog sync rewrites the endpoint and
// the models, not those. A sync with nothing to change doesn't write.
func TestOpenClawSyncKeepsTheirs(t *testing.T) {
	home, path := openClawHomeWith(t, openClawConfig)
	a := openClaw(home)
	if err := a.Pick("model", "magpie/kimi/k2"); err != nil {
		t.Fatal(err)
	}
	m := openClawParse(t, path)
	p := openClawAt_(m, "models", "providers", "magpie").(map[string]any)
	p["headers"] = map[string]any{"X-Mine": "1"}
	p["timeoutSeconds"] = 600
	p["baseUrl"] = "http://127.0.0.1:1/v1"
	for _, x := range p["models"].([]any) {
		if e := x.(map[string]any); e["id"] == "kimi/k2" {
			e["maxTokens"] = 32000
			e["reasoning"] = false
		}
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	writeFile(t, path, string(b)+"\n")
	if c := a.Check(); !strings.Contains(c, "baseUrl") {
		t.Fatalf("check with the URL changed: %q", c)
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	p = openClawAt_(openClawParse(t, path), "models", "providers", "magpie").(map[string]any)
	if p["baseUrl"] != here(home).v1() || p["timeoutSeconds"] != float64(600) || !reflect.DeepEqual(p["headers"], map[string]any{"X-Mine": "1"}) {
		t.Fatalf("after a sync: %v", p)
	}
	for _, x := range p["models"].([]any) {
		if e := x.(map[string]any); e["id"] == "kimi/k2" && (e["maxTokens"] != float64(32000) || e["reasoning"] != true) {
			t.Fatalf("k2 after a sync: %v", e)
		}
	}
	if a.Check() != "" {
		t.Fatalf("check: %s", a.Check())
	}
	st, _ := os.Stat(path)
	before, _ := os.ReadFile(path)
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	st2, _ := os.Stat(path)
	if after, _ := os.ReadFile(path); string(after) != string(before) || !st2.ModTime().Equal(st.ModTime()) {
		t.Fatal("a sync with nothing to change wrote the file")
	}
}

// openclaw.json in JSON5, which OpenClaw reads and magpie can't, is left
// as it is, and said so.
func TestOpenClawJSON5LeftAlone(t *testing.T) {
	body := "// mine\n{\n  agents: { defaults: { model: 'deepseek/deepseek-v4-pro' } },\n}\n"
	home, path := openClawHomeWith(t, body)
	a := openClaw(home)
	if err := a.Pick("model", "magpie/kimi/k2"); err == nil {
		t.Fatal("a JSON5 file was written")
	}
	if got, _ := os.ReadFile(path); string(got) != body {
		t.Fatalf("changed:\n%s", got)
	}
	if c := a.Check(); !strings.Contains(c, "JSON5") {
		t.Fatalf("check: %q", c)
	}
}

// openclaw.json is where OpenClaw itself looks: $OPENCLAW_CONFIG_PATH, its
// state folder, a named profile's folder, ~ as $OPENCLAW_HOME.
func TestOpenClawPaths(t *testing.T) {
	home := t.TempDir()
	at := here(home)
	env := map[string]string{}
	set := func(k, v string) { env[k] = v; t.Setenv(k, v) }
	for _, k := range []string{"OPENCLAW_CONFIG_PATH", "OPENCLAW_STATE_DIR", "OPENCLAW_PROFILE", "OPENCLAW_HOME"} {
		set(k, "")
	}
	check := func(wantDir, wantPath string) {
		t.Helper()
		dir, path := openClawPaths(at)
		if dir != wantDir || path != wantPath {
			t.Fatalf("%v: %s %s, want %s %s", env, dir, path, wantDir, wantPath)
		}
	}
	def := filepath.Join(home, ".openclaw")
	check(def, filepath.Join(def, "openclaw.json"))
	set("OPENCLAW_PROFILE", "default")
	check(def, filepath.Join(def, "openclaw.json"))
	set("OPENCLAW_PROFILE", "../x")
	check(def, filepath.Join(def, "openclaw.json"))
	set("OPENCLAW_PROFILE", "Work")
	w := filepath.Join(home, ".openclaw-Work")
	check(w, filepath.Join(w, "openclaw.json"))
	set("OPENCLAW_HOME", "~/oc")
	w = filepath.Join(home, "oc", ".openclaw-Work")
	check(w, filepath.Join(w, "openclaw.json"))
	set("OPENCLAW_STATE_DIR", "~/state")
	s := filepath.Join(home, "oc", "state")
	check(s, filepath.Join(s, "openclaw.json"))
	set("OPENCLAW_CONFIG_PATH", filepath.Join(home, "cfg", "my.json"))
	check(s, filepath.Join(home, "cfg", "my.json"))
}

// --- the branch's model chain, install entry and read-only policy ---

func openclawHome(t *testing.T, config string) (*Agent, func() []byte) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	for _, k := range []string{"OPENCLAW_CONFIG_PATH", "OPENCLAW_STATE_DIR", "OPENCLAW_PROFILE", "OPENCLAW_HOME", "OPENCLAW_CONFIG_READONLY", "OPENCLAW_NIX_MODE"} {
		t.Setenv(k, "")
	}
	if err := provider.Save(provider.Provider{ID: "relay", Name: "Relay", Key: "test-key", Chat: "http://127.0.0.1:1/v1", Models: []string{"m1", "m2.1"}}); err != nil {
		t.Fatal(err)
	}
	a := openClaw(home)
	if config != "" {
		if err := os.MkdirAll(filepath.Dir(a.Path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(a.Path, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return a, func() []byte {
		b, err := os.ReadFile(a.Path)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
}

// The install list must offer OpenClaw on a fresh home, without creating
// its config or pretending it is installed.
func TestOpenClawInstallOnFreshHome(t *testing.T) {
	a, _ := openclawHome(t, "")
	t.Setenv("PATH", t.TempDir())
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			for _, x := range installsOf(All(), goos, false) {
				if x.ID != a.ID {
					continue
				}
				if x.Name != a.Name || x.Icon != "openclaw-color" || len(x.Commands) != 1 || x.Missing {
					t.Fatalf("OpenClaw install entry: %+v", x)
				}
				if _, err := os.Stat(a.Dir); !os.IsNotExist(err) {
					t.Fatalf("listing created OpenClaw's config directory: %v", err)
				}
				return
			}
			t.Fatal("OpenClaw is missing from the install list on a fresh home")
		})
	}
}

func TestOpenClawRegistered(t *testing.T) {
	a, _ := openclawHome(t, "")
	t.Setenv("PATH", t.TempDir())
	found, err := Find("openclaw")
	if err != nil || found == nil || found.Path != a.Path {
		t.Fatalf("OpenClaw is missing from Agents: %v, %v", found, err)
	}
	if a.Detected() {
		t.Fatal("an uninstalled OpenClaw was detected")
	}
	if err := os.MkdirAll(a.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if !a.Detected() {
		t.Fatal("OpenClaw config folder was not detected")
	}
}

func TestOpenClawModelChain(t *testing.T) {
	for _, original := range []string{`"mine/original"`, `{"primary":"mine/original","fallbacks":["mine/backup"],"other":true}`} {
		t.Run(original, func(t *testing.T) {
			a, read := openclawHome(t, `{"agents":{"defaults":{"model":`+original+`}},"other":42}`)
			c := ModelChain{Primary: "magpie/group/auto", Fallbacks: []string{"magpie/group/fast", "anthropic/claude-opus-5"}}
			if err := a.ApplyModelChain(c); err != nil {
				t.Fatal(err)
			}
			got, err := a.ReadModelChain()
			if err != nil || got.Primary != c.Primary || !slices.Equal(got.Fallbacks, c.Fallbacks) {
				t.Fatalf("read %v %v", got, err)
			}
			c.Primary = "anthropic/claude-opus-5"
			c.Fallbacks = []string{"magpie/group/fast"}
			if err := a.ApplyModelChain(c); err != nil {
				t.Fatal(err)
			}
			if !a.Wired() {
				t.Fatal("fallback-only connection lost")
			}
			c.Fallbacks = []string{}
			if err := a.ApplyModelChain(c); err != nil {
				t.Fatal(err)
			}
			if got, _ := a.ReadModelChain(); len(got.Fallbacks) != 0 {
				t.Fatal(got)
			}
			if err := a.Disconnect(); err != nil {
				t.Fatal(err)
			}
			if !sameJSON(gjson.GetBytes(read(), openClawModel).Raw, json.RawMessage(original)) {
				t.Fatalf("restore: %s", read())
			}
			if gjson.GetBytes(read(), "other").Int() != 42 {
				t.Fatal("unrelated config changed")
			}
		})
	}
}

func TestOpenClawModelChainInvalid(t *testing.T) {
	a, read := openclawHome(t, `{"agents":{"defaults":{"model":"mine/original"}}}`)
	before := read()
	for _, c := range []ModelChain{{Primary: ""}, {Primary: "invalid"}, {Primary: "mine/a", Fallbacks: []string{"mine/a"}}, {Primary: "mine/a", Fallbacks: []string{""}}} {
		if err := a.ApplyModelChain(c); err == nil {
			t.Fatalf("accepted %+v", c)
		}
		if !bytes.Equal(before, read()) {
			t.Fatal("invalid chain changed config")
		}
	}
}

func TestOpenClawModelChainRestricted(t *testing.T) {
	a, read := openclawHome(t, `{"agents":{"defaults":{"model":"mine/original","models":{"mine/original":{}},"modelPolicy":{"allow":["mine/*"]}}}}`)
	want := ModelChain{Primary: "magpie/group/auto", Fallbacks: []string{"anthropic/claude-opus-5"}}
	if err := a.ApplyModelChain(want); err != nil {
		t.Fatal(err)
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if !gjson.GetBytes(read(), "agents.defaults.models.anthropic/claude-opus-5").Exists() {
		t.Fatal("fallback absent from restricted menu")
	}
	if !strings.Contains(gjson.GetBytes(read(), "agents.defaults.modelPolicy.allow").Raw, "anthropic/claude-opus-5") {
		t.Fatal("fallback excluded by policy")
	}
	out, err := edit.PatchJSON(read(), []edit.KV{{Path: openClawModel + ".fallbacks", Value: []string{"mine/changed"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.Path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(read(), openClawModel+".fallbacks.0").String() != "mine/changed" {
		t.Fatal("external edit lost")
	}
}

func TestOpenClawAgentModelOverride(t *testing.T) {
	for _, tc := range []struct{ roster, target string }{
		{`"entries":{"main":{"model":{"primary":"mine/override"}},"other":{"model":"other/keep"}},"defaults":{"systemAgent":{"agentId":"main"},"model":{"primary":"mine/default","fallbacks":["mine/backup"]}}`, "agents.entries.main.model"},
		{`"entries":{"main":{"model":"mine/override"}},"defaults":{"model":{"primary":"mine/default","fallbacks":["mine/backup"]}}`, "agents.entries.main.model"},
		{`"list":[{"id":"other","model":"other/keep"},{"id":"main","default":true,"model":{"primary":"mine/override"}}],"defaults":{"model":{"primary":"mine/default","fallbacks":["mine/backup"]}}`, "agents.list.1.model"},
	} {
		t.Run(tc.target+tc.roster, func(t *testing.T) {
			a, read := openclawHome(t, `{"agents":{`+tc.roster+`}}`)
			before := gjson.GetBytes(read(), tc.target).Raw
			c, err := a.ReadModelChain()
			if err != nil || c.Primary != "mine/override" || len(c.Fallbacks) != 0 {
				t.Fatalf("effective model: %+v, %v", c, err)
			}
			c = ModelChain{Primary: "magpie/group/auto", Fallbacks: []string{}}
			if err := a.ApplyModelChain(c); err != nil {
				t.Fatal(err)
			}
			if got := gjson.GetBytes(read(), tc.target+".primary").String(); got != c.Primary {
				t.Fatalf("override still wins: %s", got)
			}
			got, err := a.ReadModelChain()
			if err != nil || got.Primary != c.Primary || len(got.Fallbacks) != 0 {
				t.Fatalf("effective saved chain: %+v %v", got, err)
			}
			if err := a.Disconnect(); err != nil {
				t.Fatal(err)
			}
			if !sameJSON(gjson.GetBytes(read(), tc.target).Raw, json.RawMessage(before)) {
				t.Fatalf("restore override: %s", read())
			}
		})
	}
}

func TestOpenClawWSL(t *testing.T) {
	a, _ := openclawHome(t, "")
	home := filepath.Dir(a.Dir)
	t.Setenv("OPENCLAW_CONFIG_PATH", filepath.Join(t.TempDir(), "windows.json"))
	t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(t.TempDir(), "windows-state"))
	t.Setenv("OPENCLAW_HOME", t.TempDir())
	t.Setenv("OPENCLAW_CONFIG_READONLY", "1")
	t.Setenv("OPENCLAW_NIX_MODE", "1")
	wsl := openclawIn(place{home: home, id: "openclaw@wsl:Ubuntu", spell: func(p string) string { return p }, base: func() string { return "http://172.22.0.1:20186" }})
	if wsl.Path != a.Path {
		t.Fatal("WSL read Windows' config variable")
	}
	if err := wsl.Field("model").Set("magpie/relay/m1"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(wsl.Path)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(data, openClawProvider+".baseUrl").String() != "http://172.22.0.1:20186/v1" || wsl.Check() != "" {
		t.Fatalf("WSL gateway is wrong: %s", data)
	}
	if err := wsl.Field("model").Set(""); err != nil {
		t.Fatal(err)
	}
}

func TestOpenClawPathsAndReadOnly(t *testing.T) {
	a, _ := openclawHome(t, "")
	home := filepath.Dir(a.Dir)
	other := t.TempDir()
	t.Setenv("OPENCLAW_HOME", other)
	if got := openClaw(home).Dir; got != filepath.Join(other, ".openclaw") {
		t.Fatalf("OPENCLAW_HOME ignored: %s", got)
	}
	t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(other, "state"))
	if got := openClaw(home).Path; got != filepath.Join(other, "state", "openclaw.json") {
		t.Fatalf("OPENCLAW_STATE_DIR ignored: %s", got)
	}
	t.Setenv("OPENCLAW_CONFIG_PATH", filepath.Join(other, "custom.json"))
	a = openClaw(home)
	if a.Path != filepath.Join(other, "custom.json") {
		t.Fatal("OPENCLAW_CONFIG_PATH ignored")
	}
	for _, env := range []string{"OPENCLAW_CONFIG_READONLY", "OPENCLAW_NIX_MODE"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, "1")
			if err := a.Field("model").Set("magpie/relay/m1"); err == nil {
				t.Fatal("read-only config was written")
			}
			if _, err := os.Stat(a.Path); !os.IsNotExist(err) {
				t.Fatal("read-only config was created")
			}
		})
	}
}
