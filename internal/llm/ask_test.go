package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeTools struct{ got SearchArgs }

func (f *fakeTools) Search(a SearchArgs) any     { f.got = a; return map[string]int{"total_repos": 1} }
func (f *fakeTools) ListValues(field string) any { return []string{field} }

func TestRunToolValidates(t *testing.T) {
	f := &fakeTools{}
	out := runTool(f, "search", `{"kinds":["decomp","DROP TABLE"],"platforms_any":["Steam Deck","bogus"],"group_by":"evil","limit":9999}`)
	if out != `{"total_repos":1}` {
		t.Errorf("search output %s", out)
	}
	if len(f.got.Kinds) != 1 || f.got.Kinds[0] != "decomp" || f.got.GroupBy != "" || f.got.Limit != MaxSearchLimit ||
		len(f.got.PlatformsAny) != 1 || f.got.PlatformsAny[0] != "linux" {
		t.Errorf("args not sanitized: %+v", f.got)
	}
	if out := runTool(f, "list_values", `{"field":"sqlite_master"}`); !strings.Contains(out, "error") {
		t.Errorf("bad field accepted: %s", out)
	}
	if out := runTool(f, "exec_sql", `{}`); !strings.Contains(out, "unknown tool") {
		t.Errorf("unknown tool: %s", out)
	}
	if out := runTool(f, "search", `not json`); !strings.Contains(out, "invalid arguments") {
		t.Errorf("bad json: %s", out)
	}
}

func TestAskLoop(t *testing.T) {
	var requests []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		requests = append(requests, body)
		w.Header().Set("Content-Type", "application/json")
		if len(requests) == 1 {
			fmt.Fprint(w, `{"id":"1","object":"chat.completion","created":0,"model":"m","choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"search","arguments":"{\"game_contains\":\"\",\"titles\":[],\"systems\":[],\"kinds\":[\"decomp\"],\"platforms_any\":[],\"platforms_all\":[],\"platforms_none\":[],\"group_by\":\"\",\"limit\":0}"}}]}}]}`)
			return
		}
		fmt.Fprint(w, `{"id":"2","object":"chat.completion","created":0,"model":"m","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"There is 1 decomp."}}]}`)
	}))
	defer srv.Close()
	f := &fakeTools{}
	answer, err := New("key", "m", srv.URL, "").Ask(context.Background(), "how many decomps?", f)
	if err != nil || answer != "There is 1 decomp." {
		t.Fatalf("answer %q, err %v", answer, err)
	}
	if len(requests) != 2 || len(f.got.Kinds) != 1 {
		t.Fatalf("requests %d, args %+v", len(requests), f.got)
	}
	msgs := requests[1]["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "tool" || last["tool_call_id"] != "call_1" || last["content"] != `{"total_repos":1}` {
		t.Errorf("tool result message: %v", last)
	}
	if prev := msgs[len(msgs)-2].(map[string]any); prev["tool_calls"] == nil {
		t.Errorf("assistant tool call not echoed back: %v", prev)
	}
}
