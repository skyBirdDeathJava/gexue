package rerank

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDashScopeRerank(t *testing.T) {
	var gotReq rerankReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/text-rerank" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing bearer auth: %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("decode request: %v", err)
		}
		// 返回乱序结果，验证调用方按 score 排序
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"output":{"results":[
			{"index":1,"relevance_score":0.17},
			{"index":0,"relevance_score":0.48}
		]},"usage":{"total_tokens":10}}`))
	}))
	defer srv.Close()

	d := NewDashScopeWithURL("test-key", "qwen3-rerank", srv.URL+"/text-rerank")
	res, err := d.Rerank(context.Background(), "什么是一元二次方程",
		[]string{"一元二次方程的解法", "今天天气很好"}, 2)
	if err != nil {
		t.Fatalf("Rerank err: %v", err)
	}

	// 请求体校验
	if gotReq.Model != "qwen3-rerank" {
		t.Errorf("model = %q, want qwen3-rerank", gotReq.Model)
	}
	if gotReq.Input.Query != "什么是一元二次方程" {
		t.Errorf("query = %q", gotReq.Input.Query)
	}
	if len(gotReq.Input.Documents) != 2 || gotReq.Input.Documents[0] != "一元二次方程的解法" {
		t.Errorf("documents = %v", gotReq.Input.Documents)
	}
	if gotReq.Parameters.TopN != 2 {
		t.Errorf("top_n = %d, want 2", gotReq.Parameters.TopN)
	}

	// 响应校验：index 映射 + 降序
	if len(res) != 2 {
		t.Fatalf("results len = %d, want 2", len(res))
	}
	if res[0].Index != 0 || res[0].Score != 0.48 {
		t.Errorf("top result = %+v, want index 0 score 0.48", res[0])
	}
	if res[1].Index != 1 || res[1].Score != 0.17 {
		t.Errorf("second result = %+v, want index 1 score 0.17", res[1])
	}
}

func TestDashScopeRerankErrors(t *testing.T) {
	t.Run("empty api key", func(t *testing.T) {
		d := NewDashScope("", "qwen3-rerank")
		if _, err := d.Rerank(context.Background(), "q", []string{"a"}, 1); err == nil {
			t.Error("expected error without api key")
		}
	})
	t.Run("empty documents", func(t *testing.T) {
		d := NewDashScope("k", "qwen3-rerank")
		res, err := d.Rerank(context.Background(), "q", nil, 1)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(res) != 0 {
			t.Errorf("expected empty results, got %v", res)
		}
	})
	t.Run("non-200 response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer srv.Close()
		d := NewDashScopeWithURL("k", "qwen3-rerank", srv.URL)
		if _, err := d.Rerank(context.Background(), "q", []string{"a"}, 1); err == nil {
			t.Error("expected error on non-200")
		}
	})
	t.Run("bad json response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{invalid`))
		}))
		defer srv.Close()
		d := NewDashScopeWithURL("k", "qwen3-rerank", srv.URL)
		if _, err := d.Rerank(context.Background(), "q", []string{"a"}, 1); err == nil {
			t.Error("expected error on bad json")
		}
	})
}
