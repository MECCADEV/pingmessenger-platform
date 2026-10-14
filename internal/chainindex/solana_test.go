package chainindex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSolanaRPCExtractsNativeAndTokenTransfers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected RPC request: %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"slot":42,"meta":{"err":null,"fee":5000,"preTokenBalances":[{"accountIndex":2,"mint":"Mint111","owner":"Alice","uiTokenAmount":{"amount":"100","decimals":6}}],"postTokenBalances":[{"accountIndex":3,"mint":"Mint111","owner":"Bob","uiTokenAmount":{"amount":"100","decimals":6}}],"innerInstructions":[{"index":1,"instructions":[{"program":"system","parsed":{"type":"transfer","info":{"source":"AliceSOL","destination":"BobSOL","lamports":9}}}]}]},"transaction":{"message":{"accountKeys":[{"pubkey":"FeePayer"},{"pubkey":"AliceSOL"},{"pubkey":"TokenSource"},{"pubkey":"TokenDest"}],"instructions":[{"program":"system","parsed":{"type":"transfer","info":{"source":"AliceSOL","destination":"BobSOL","lamports":12345}}},{"program":"spl-token","parsed":{"type":"transferChecked","info":{"source":"TokenSource","destination":"TokenDest","mint":"Mint111","tokenAmount":{"amount":"100","decimals":6}}}}]}}}}`))
	}))
	defer server.Close()
	tx, err := (&SolanaRPC{Endpoint: server.URL, Client: server.Client()}).Fetch(context.Background(), "solana-devnet", "sig")
	if err != nil {
		t.Fatal(err)
	}
	if tx.Status != "confirmed" || tx.BlockID != "42" || tx.FeeAtomic != "5000" || tx.FeePayer != "FeePayer" {
		t.Fatalf("unexpected transaction: %+v", tx)
	}
	if len(tx.Transfers) != 3 {
		t.Fatalf("transfer count = %d, want 2: %+v", len(tx.Transfers), tx.Transfers)
	}
	if tx.Transfers[0].Type != "native" || tx.Transfers[0].AmountAtomic != "12345" || tx.Transfers[0].From != "AliceSOL" || tx.Transfers[0].To != "BobSOL" {
		t.Fatalf("native transfer = %+v", tx.Transfers[0])
	}
	if tx.Transfers[1].Type != "token" || tx.Transfers[1].Mint != "Mint111" || tx.Transfers[1].AmountAtomic != "100" || tx.Transfers[1].Decimals != 6 {
		t.Fatalf("token transfer = %+v", tx.Transfers[1])
	}
	if tx.Transfers[2].AmountAtomic != "9" || tx.Transfers[2].InnerInstructionIndex != 0 {
		t.Fatalf("inner transfer = %+v", tx.Transfers[2])
	}
}

func TestSolanaRPCReportsChainFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"slot":7,"meta":{"err":{"InstructionError":[0,"Custom"]},"fee":1},"transaction":{"message":{"accountKeys":[],"instructions":[]}}}}`))
	}))
	defer server.Close()
	tx, err := (&SolanaRPC{Endpoint: server.URL}).Fetch(context.Background(), "solana-devnet", "sig")
	if err != nil {
		t.Fatal(err)
	}
	if tx.Status != "failed" || !strings.Contains(tx.ErrorCode, "InstructionError") {
		t.Fatalf("unexpected failed transaction: %+v", tx)
	}
}
