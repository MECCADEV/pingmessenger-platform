package chainindex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	json "github.com/goccy/go-json"
)

// SolanaRPC is a small standard JSON-RPC adapter. It requests jsonParsed
// transactions so native System transfers and SPL Token transfers are
// extracted from parsed instruction data instead of trusting client payloads.
type SolanaRPC struct {
	Endpoint string
	Client   *http.Client
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type solanaResult struct {
	Slot        int64      `json:"slot"`
	Meta        solanaMeta `json:"meta"`
	Transaction solanaTx   `json:"transaction"`
}
type solanaMeta struct {
	Err               json.RawMessage     `json:"err"`
	Fee               uint64              `json:"fee"`
	PreTokenBalances  []tokenBalance      `json:"preTokenBalances"`
	PostTokenBalances []tokenBalance      `json:"postTokenBalances"`
	InnerInstructions []innerInstructions `json:"innerInstructions"`
}
type innerInstructions struct {
	Index        int           `json:"index"`
	Instructions []instruction `json:"instructions"`
}
type tokenBalance struct {
	AccountIndex  int    `json:"accountIndex"`
	Mint          string `json:"mint"`
	Owner         string `json:"owner"`
	UiTokenAmount struct {
		Amount   string `json:"amount"`
		Decimals int    `json:"decimals"`
	} `json:"uiTokenAmount"`
}
type solanaTx struct {
	Message struct {
		AccountKeys  []accountKey  `json:"accountKeys"`
		Instructions []instruction `json:"instructions"`
	} `json:"message"`
}
type accountKey struct {
	Pubkey string `json:"pubkey"`
}
type instruction struct {
	Program string `json:"program"`
	Parsed  *struct {
		Type string         `json:"type"`
		Info map[string]any `json:"info"`
	} `json:"parsed,omitempty"`
}

func (s *SolanaRPC) Fetch(ctx context.Context, chainID, transactionID string) (*Transaction, error) {
	if s == nil || strings.TrimSpace(s.Endpoint) == "" || strings.TrimSpace(transactionID) == "" {
		return nil, errors.New("solana RPC is not configured")
	}
	if !isSolanaChain(chainID) {
		return nil, fmt.Errorf("no Solana adapter for chain %q", chainID)
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "getTransaction",
		"params": []any{transactionID, map[string]any{"encoding": "jsonParsed", "commitment": "finalized", "maxSupportedTransactionVersion": 0}},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("solana RPC request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("solana RPC status %s", resp.Status)
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var envelope rpcEnvelope
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return nil, fmt.Errorf("decode solana RPC: %w", err)
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("solana RPC %d: %s", envelope.Error.Code, envelope.Error.Message)
	}
	if string(envelope.Result) == "null" || len(envelope.Result) == 0 {
		return nil, fmt.Errorf("transaction %s not found", transactionID)
	}
	var result solanaResult
	if err := json.Unmarshal(envelope.Result, &result); err != nil {
		return nil, fmt.Errorf("decode transaction: %w", err)
	}
	status := "confirmed"
	errorCode := ""
	if len(result.Meta.Err) > 0 && string(result.Meta.Err) != "null" {
		status = "failed"
		errorCode = string(result.Meta.Err)
	}
	blockID := strconv.FormatInt(result.Slot, 10)
	tx := &Transaction{ChainID: chainID, TransactionID: transactionID, BlockID: blockID, Slot: &result.Slot, Status: status, FeeAtomic: strconv.FormatUint(result.Meta.Fee, 10), ErrorCode: errorCode, Transfers: parseTransfers(result)}
	if len(responseBody) <= 2<<20 {
		tx.RawPayload = append([]byte(nil), envelope.Result...)
	}
	if len(result.Transaction.Message.AccountKeys) > 0 {
		tx.FeePayer = result.Transaction.Message.AccountKeys[0].Pubkey
	}
	return tx, nil
}

func isSolanaChain(chainID string) bool {
	switch strings.ToLower(strings.TrimSpace(chainID)) {
	case "solana", "solana-devnet", "solana-testnet", "solana-mainnet", "solana-mainnet-beta":
		return true
	default:
		return false
	}
}

func parseTransfers(result solanaResult) []Transfer {
	accountKeys := make([]string, len(result.Transaction.Message.AccountKeys))
	for i, key := range result.Transaction.Message.AccountKeys {
		accountKeys[i] = key.Pubkey
	}
	owners := make(map[string]tokenBalance)
	for _, balance := range append(result.Meta.PreTokenBalances, result.Meta.PostTokenBalances...) {
		if _, ok := owners[accountAddress(accountKeys, balance.AccountIndex)]; !ok {
			owners[accountAddress(accountKeys, balance.AccountIndex)] = balance
		}
	}
	var transfers []Transfer
	appendTransfer := func(item instruction, instructionIndex, innerIndex int) {
		if item.Parsed == nil {
			return
		}
		if item.Parsed.Type != "transfer" && item.Parsed.Type != "transferChecked" {
			return
		}
		info := item.Parsed.Info
		from, _ := info["source"].(string)
		to, _ := info["destination"].(string)
		if lamports, ok := uintString(info["lamports"]); ok {
			transfers = append(transfers, Transfer{Index: len(transfers), Type: "native", Asset: "SOL", From: from, To: to, AmountAtomic: lamports, Decimals: 9, InstructionIndex: instructionIndex, InnerInstructionIndex: innerIndex})
			return
		}
		amount, decimals := "", 0
		if tokenAmount, ok := info["tokenAmount"].(map[string]any); ok {
			amount, _ = tokenAmount["amount"].(string)
			if value, ok := tokenAmount["decimals"].(float64); ok {
				decimals = int(value)
			}
		}
		if amount == "" {
			amount, _ = info["amount"].(string)
		}
		if amount == "" {
			return
		}
		mint, _ := info["mint"].(string)
		if owner := owners[from]; mint == "" {
			mint = owner.Mint
			if decimals == 0 {
				decimals = owner.UiTokenAmount.Decimals
			}
		}
		if owner := owners[to]; mint == "" {
			mint = owner.Mint
			if decimals == 0 {
				decimals = owner.UiTokenAmount.Decimals
			}
		}
		transfers = append(transfers, Transfer{Index: len(transfers), Type: "token", Asset: mint, Mint: mint, From: tokenOwner(owners, from), To: tokenOwner(owners, to), AmountAtomic: amount, Decimals: decimals, InstructionIndex: instructionIndex, InnerInstructionIndex: innerIndex})
	}
	for index, item := range result.Transaction.Message.Instructions {
		appendTransfer(item, index, -1)
	}
	for _, group := range result.Meta.InnerInstructions {
		for innerIndex, item := range group.Instructions {
			appendTransfer(item, group.Index, innerIndex)
		}
	}
	return transfers
}

func accountAddress(keys []string, index int) string {
	if index >= 0 && index < len(keys) {
		return keys[index]
	}
	return ""
}
func tokenOwner(owners map[string]tokenBalance, address string) string {
	if owner := owners[address].Owner; owner != "" {
		return owner
	}
	return address
}
func uintString(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		if v != "" {
			return v, true
		}
	case float64:
		if v >= 0 && v == float64(uint64(v)) {
			return strconv.FormatUint(uint64(v), 10), true
		}
	case json.Number:
		return v.String(), true
	}
	return "", false
}
