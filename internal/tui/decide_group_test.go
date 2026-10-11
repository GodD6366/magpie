package tui

import (
	"slices"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// The member picker offers a group of decision models only decision
// models, a chat group none, and a new group both (ARNO on Discord).
func TestMemberOptionsDecisionGroup(t *testing.T) {
	home(t)
	if err := provider.Save(provider.Provider{ID: "ja", Name: "JA", Key: "k1", Decide: "http://127.0.0.1:1/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Save(provider.Provider{ID: "jb", Name: "JB", Key: "k2", Decide: "http://127.0.0.1:1/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveGroup(provider.Group{ID: "jevs", Name: "Jevs", Members: []string{"ja/jev-latest"}}); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveGroup(provider.Group{ID: "chat", Name: "Chat", Members: []string{"a/m"}}); err != nil {
		t.Fatal(err)
	}
	ids := func(skip []string, self string) []string {
		var out []string
		for _, o := range memberOptions(skip, self) {
			out = append(out, o.Value)
		}
		return out
	}
	got := ids([]string{"ja/jev-latest"}, "jevs")
	if !slices.Contains(got, "jb/jev-latest") || slices.Contains(got, "ja/jev-latest") || slices.ContainsFunc(got, func(id string) bool { return !provider.IsDecider(id) }) {
		t.Fatalf("decision group offered %v", got)
	}
	if got := ids([]string{"a/m"}, "chat"); slices.Contains(got, "jb/jev-latest") || slices.Contains(got, "group/jevs") || len(got) == 0 {
		t.Fatalf("chat group offered %v", got)
	}
	if got := ids(nil, ""); !slices.Contains(got, "jb/jev-latest") || !slices.Contains(got, "a/m") {
		t.Fatalf("a new group offered %v", got)
	}
}
