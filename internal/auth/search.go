package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// UserSearchResult contains only directory data that is safe to expose to
// another authenticated user. Contact values are intentionally omitted.
type UserSearchResult struct {
	OpenIMUserID string
	Username     string
	PingID       string
	Nickname     string
	Image        *string
}

// SearchUsers searches the active, OpenIM-provisioned account directory. The
// field is deliberately a closed set so the SQL remains parameterized rather
// than interpolating a caller-controlled column or predicate.
func (r *UserRepository) SearchUsers(ctx context.Context, requesterID, field, query string, limit int) ([]*UserSearchResult, error) {
	field = strings.ToLower(strings.TrimSpace(field))
	query = strings.TrimSpace(query)
	if requesterID == "" || query == "" || limit < 1 || limit > 50 {
		return nil, fmt.Errorf("invalid user search")
	}

	const selectSQL = `SELECT u.openim_user_id,COALESCE(u.username,''),u.ping_id,COALESCE(u.nickname,''),(
		SELECT public_path FROM profile_assets
		WHERE user_id=u.id AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1
	) FROM users u`
	var rows pgx.Rows
	var err error
	switch field {
	case "username":
		// Escape LIKE metacharacters so a username search cannot turn into an
		// unbounded wildcard query. The surrounding % enables partial matching.
		pattern := "%" + escapeSearchLike(query) + "%"
		rows, err = r.pool.Query(ctx, selectSQL+` WHERE u.id<>$1 AND u.status='active' AND u.deleted_at IS NULL AND u.openim_user_id IS NOT NULL AND u.username IS NOT NULL AND u.username ILIKE $2 ESCAPE '\' ORDER BY u.username ASC,u.id ASC LIMIT $3`, requesterID, pattern, limit)
	case "email", "phone":
		value := query
		if field == "email" {
			value = strings.ToLower(value)
		}
		rows, err = r.pool.Query(ctx, selectSQL+` JOIN user_contacts c ON c.user_id=u.id WHERE u.id<>$1 AND u.status='active' AND u.deleted_at IS NULL AND u.openim_user_id IS NOT NULL AND c.kind=$2::contact_kind AND c.value_normalized=$3 AND c.verified_at IS NOT NULL ORDER BY u.updated_at DESC,u.id ASC LIMIT $4`, requesterID, field, value, limit)
	case "ping_id":
		rows, err = r.pool.Query(ctx, selectSQL+` WHERE u.id<>$1 AND u.status='active' AND u.deleted_at IS NULL AND u.openim_user_id IS NOT NULL AND u.ping_id=$2 ORDER BY u.updated_at DESC,u.id ASC LIMIT $3`, requesterID, query, limit)
	default:
		return nil, fmt.Errorf("unsupported search field")
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]*UserSearchResult, 0)
	for rows.Next() {
		result := new(UserSearchResult)
		if err := rows.Scan(&result.OpenIMUserID, &result.Username, &result.PingID, &result.Nickname, &result.Image); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func escapeSearchLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}
