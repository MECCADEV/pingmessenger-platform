// Package chainindex stores normalized observations extracted from chain
// transactions. The interface is chain-neutral; adapters can be added without
// changing the payment ledger or HTTP contract.
package chainindex

import "context"

type Transfer struct {
	Index                 int    `json:"index"`
	Type                  string `json:"type"`
	Asset                 string `json:"asset,omitempty"`
	Mint                  string `json:"mint,omitempty"`
	From                  string `json:"from,omitempty"`
	To                    string `json:"to,omitempty"`
	AmountAtomic          string `json:"amount_atomic"`
	Decimals              int    `json:"decimals"`
	InstructionIndex      int    `json:"instruction_index,omitempty"`
	InnerInstructionIndex int    `json:"inner_instruction_index,omitempty"`
}

type Transaction struct {
	ChainID       string     `json:"chain_id"`
	TransactionID string     `json:"transaction_id"`
	BlockID       string     `json:"block_id,omitempty"`
	Slot          *int64     `json:"slot,omitempty"`
	Status        string     `json:"status"`
	FeeAtomic     string     `json:"fee_atomic,omitempty"`
	FeePayer      string     `json:"fee_payer,omitempty"`
	ErrorCode     string     `json:"error_code,omitempty"`
	RawPayload    []byte     `json:"-"`
	Transfers     []Transfer `json:"transfers"`
}

// Indexer fetches and parses a transaction from a chain RPC.
type Indexer interface {
	Fetch(context.Context, string, string) (*Transaction, error)
}
