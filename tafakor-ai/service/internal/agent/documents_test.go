package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tafakorpardazan/tafakor-ai/internal/accounting"
)

func TestJournalAndStockDocumentsArePrintable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/journal-entries/5":
			_, _ = w.Write([]byte(`{"number":12,"date":"2025-10-08","status":"posted","source_type":"manual","description":"اصلاح","total_debit":"100","total_credit":"100",
				"lines":[{"account_code":"1101","account_name":"صندوق","debit":"100","credit":"0"},{"account_code":"3101","account_name":"سرمایه","debit":"0","credit":"100"}]}`))
		case "/api/v1/inventory/documents/7":
			_, _ = w.Write([]byte(`{"kind":"receipt","number":3,"date":"2025-10-08","warehouse_name":"مرکزی","status":"posted",
				"lines":[{"product_name":"لپ‌تاپ","product_sku":"LP1","unit":"عدد","quantity":"2","unit_cost":"500"}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	a := &Agent{Acc: accounting.New(srv.URL)}
	u := &User{Cred: accounting.Credentials{Session: "s", CSRF: "c"}}
	for _, tc := range []struct {
		art   Artifact
		title string
		cells string
	}{
		{Artifact{Type: "journal_entry", ID: 5}, "سند حسابداری 12", "سرمایه"},
		{Artifact{Type: "stock_document", ID: 7}, "رسید انبار", "لپ‌تاپ"},
	} {
		if !printable(tc.art) {
			t.Fatalf("%s not printable", tc.art.Type)
		}
		doc, ok, err := a.moreDocuments(context.Background(), u, "fa", tc.art.target())
		if !ok || err != nil {
			t.Fatalf("%s: ok=%v err=%v", tc.art.Type, ok, err)
		}
		var m map[string]any
		_ = json.Unmarshal(doc, &m)
		if m["title"] != tc.title || !strings.Contains(string(doc), tc.cells) || m["date"] != "1404/07/16" {
			t.Fatalf("%s document: %s", tc.art.Type, doc)
		}
	}
	if _, ok, _ := a.moreDocuments(context.Background(), u, "fa", &Target{EntityType: "party", EntityID: 1}); ok {
		t.Fatal("a party is not a printable document")
	}
}
