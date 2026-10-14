package chainindex

import (
	"context"
	"errors"
	"fmt"

	json "github.com/goccy/go-json"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// Upsert replaces the transfer projection for a transaction. Replaying an RPC
// response is therefore safe and cannot duplicate individual transfers.
func (r *Repository) Upsert(ctx context.Context, txData *Transaction) (string, error) {
	if txData == nil || txData.ChainID == "" || txData.TransactionID == "" || (txData.Status != "pending" && txData.Status != "confirmed" && txData.Status != "failed") {
		return "", errors.New("invalid chain transaction")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var id string
	_, err = tx.Exec(ctx, `
		INSERT INTO chain_transactions(chain_id,transaction_id,block_id,slot,status,fee_atomic,fee_payer,error_code,raw_payload,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,now())
		ON CONFLICT (chain_id,transaction_id) DO UPDATE SET block_id=EXCLUDED.block_id,slot=EXCLUDED.slot,status=EXCLUDED.status,fee_atomic=EXCLUDED.fee_atomic,fee_payer=EXCLUDED.fee_payer,error_code=EXCLUDED.error_code,raw_payload=COALESCE(EXCLUDED.raw_payload,chain_transactions.raw_payload),updated_at=now()
		`, txData.ChainID, txData.TransactionID, txData.BlockID, txData.Slot, txData.Status, txData.FeeAtomic, txData.FeePayer, txData.ErrorCode, nullableJSON(txData.RawPayload))
	if err != nil {
		return "", err
	}
	// pgx Exec does not expose RETURNING rows; query the stable unique key.
	if err = tx.QueryRow(ctx, `SELECT id::text FROM chain_transactions WHERE chain_id=$1 AND transaction_id=$2 FOR UPDATE`, txData.ChainID, txData.TransactionID).Scan(&id); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM chain_transfers WHERE chain_transaction_id=$1`, id); err != nil {
		return "", err
	}
	for _, transfer := range txData.Transfers {
		if transfer.AmountAtomic == "" || (transfer.Type != "native" && transfer.Type != "token") {
			return "", errors.New("invalid chain transfer")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO chain_transfers(chain_transaction_id,transfer_index,transfer_type,asset,mint,from_address,to_address,amount_atomic,decimals,instruction_index,inner_instruction_index) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, id, transfer.Index, transfer.Type, transfer.Asset, transfer.Mint, transfer.From, transfer.To, transfer.AmountAtomic, transfer.Decimals, transfer.InstructionIndex, transfer.InnerInstructionIndex); err != nil {
			return "", err
		}
	}
	// Link every matching payment without changing its client-requested amount
	// or lifecycle status. A later watcher event remains authoritative for status.
	if _, err = tx.Exec(ctx, `UPDATE payments SET chain_transaction_id=$3,block_id=CASE WHEN $4 <> '' THEN $4 ELSE block_id END,updated_at=now() WHERE chain_id=$1 AND transaction_id=$2`, txData.ChainID, txData.TransactionID, id, txData.BlockID); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}

func nullableJSON(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return raw
}

type TransferList struct {
	Transaction *Transaction `json:"transaction"`
	Transfers   []Transfer   `json:"transfers"`
}

func (r *Repository) GetForPayment(ctx context.Context, paymentID string) (*TransferList, error) {
	var txData Transaction
	var slot *int64
	err := r.pool.QueryRow(ctx, `SELECT t.chain_id,t.transaction_id,t.block_id,t.slot,t.status,t.fee_atomic,t.fee_payer,t.error_code FROM chain_transactions t JOIN payments p ON p.chain_transaction_id=t.id WHERE p.payment_id=$1`, paymentID).Scan(&txData.ChainID, &txData.TransactionID, &txData.BlockID, &slot, &txData.Status, &txData.FeeAtomic, &txData.FeePayer, &txData.ErrorCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("chain transaction not indexed")
	}
	if err != nil {
		return nil, err
	}
	txData.Slot = slot
	rows, err := r.pool.Query(ctx, `SELECT transfer_index,transfer_type,asset,mint,from_address,to_address,amount_atomic,decimals,instruction_index,inner_instruction_index FROM chain_transfers WHERE chain_transaction_id=(SELECT chain_transaction_id FROM payments WHERE payment_id=$1) ORDER BY transfer_index`, paymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var transfer Transfer
		if err := rows.Scan(&transfer.Index, &transfer.Type, &transfer.Asset, &transfer.Mint, &transfer.From, &transfer.To, &transfer.AmountAtomic, &transfer.Decimals, &transfer.InstructionIndex, &transfer.InnerInstructionIndex); err != nil {
			return nil, err
		}
		txData.Transfers = append(txData.Transfers, transfer)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &TransferList{Transaction: &txData, Transfers: txData.Transfers}, nil
}

// IndexerService coordinates an RPC adapter and the raw-pgx projection.
type IndexerService struct {
	indexer Indexer
	repo    *Repository
}

func NewService(indexer Indexer, repo *Repository) *IndexerService {
	return &IndexerService{indexer: indexer, repo: repo}
}

func (s *IndexerService) Index(ctx context.Context, chainID, transactionID string) (*Transaction, error) {
	if s == nil || s.indexer == nil || s.repo == nil {
		return nil, fmt.Errorf("chain indexer is not configured")
	}
	txData, err := s.indexer.Fetch(ctx, chainID, transactionID)
	if err != nil {
		return nil, err
	}
	if _, err := s.repo.Upsert(ctx, txData); err != nil {
		return nil, err
	}
	return txData, nil
}

func (s *IndexerService) GetForPayment(ctx context.Context, paymentID string) (*TransferList, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("chain indexer is not configured")
	}
	return s.repo.GetForPayment(ctx, paymentID)
}
