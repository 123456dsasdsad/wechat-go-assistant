// Package accountupload imports credentials deterministically, without a model.
package accountupload

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strings"
)

const MaxUploadBytes = 32 << 20
const MaxAccounts = 5000

type Account struct {
	Email        string `json:"email"`
	AccountID    string `json:"account_id"`
	UserID       string `json:"user_id,omitempty"`
	PlanType     string `json:"plan_type,omitempty"`
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

func decode(data []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
	d.UseNumber()
	if e := d.Decode(value); e != nil {
		return errors.New("invalid_json")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("invalid_json")
	}
	return nil
}
func text(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func claims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(parts[1]) > 128<<10 {
		return nil
	}
	raw, e := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if e != nil {
		return nil
	}
	var m map[string]any
	if decode(raw, &m) != nil {
		return nil
	}
	return m
}
func cleanField(v string, max int) bool { return len(v) <= max && !strings.ContainsAny(v, "\r\n\x00") }
func validAccount(a Account) bool {
	email, e := mail.ParseAddress(a.Email)
	return e == nil && email.Address == a.Email && strings.Contains(a.Email, "@") && cleanField(a.Email, 254) && a.AccountID != "" && cleanField(a.AccountID, 256) && cleanField(a.UserID, 256) && cleanField(a.PlanType, 80) && a.AccessToken != "" && a.IDToken != "" && cleanField(a.AccessToken, 128<<10) && cleanField(a.IDToken, 128<<10) && cleanField(a.RefreshToken, 128<<10)
}
func identity(a Account) string {
	user := a.UserID
	if user == "" {
		user = strings.ToLower(a.Email)
	}
	return a.AccountID + "\x00" + user
}

func Parse(data []byte) ([]Account, int, error) {
	if len(data) == 0 || len(data) > MaxUploadBytes {
		return nil, 0, errors.New("invalid_json_size")
	}
	var value any
	if e := decode(data, &value); e != nil {
		return nil, 0, e
	}
	var rows []any
	switch v := value.(type) {
	case []any:
		rows = v
	case map[string]any:
		if list, ok := v["accounts"].([]any); ok {
			rows = list
		} else {
			rows = []any{v}
		}
	default:
		return nil, 0, errors.New("invalid_account_format")
	}
	if len(rows) == 0 || len(rows) > MaxAccounts {
		return nil, 0, errors.New("invalid_account_count")
	}
	result := make([]Account, 0, len(rows))
	seen := map[string]int{}
	duplicates := 0
	for i, row := range rows {
		m := object(row)
		if m == nil {
			return nil, 0, fmt.Errorf("invalid_account_row_%d", i+1)
		}
		if kind := text(m, "type"); kind != "" && kind != "codex" {
			return nil, 0, fmt.Errorf("unsupported_account_row_%d", i+1)
		}
		if mode := text(m, "auth_mode"); mode != "" && mode != "oauth" && mode != "chatgpt" {
			return nil, 0, fmt.Errorf("unsupported_account_row_%d", i+1)
		}
		tokens := object(m["tokens"])
		if tokens == nil {
			tokens = m
		}
		a := Account{AccessToken: text(tokens, "access_token", "accessToken"), IDToken: text(tokens, "id_token", "idToken"), RefreshToken: text(tokens, "refresh_token", "refreshToken"), Email: strings.ToLower(text(m, "email")), AccountID: text(m, "account_id", "accountId"), UserID: text(m, "user_id", "userId"), PlanType: text(m, "plan_type", "planType")}
		access, id := claims(a.AccessToken), claims(a.IDToken)
		auth := object(access["https://api.openai.com/auth"])
		if auth == nil {
			auth = object(id["https://api.openai.com/auth"])
		}
		tokenID := text(auth, "chatgpt_account_id", "account_id")
		if a.AccountID == "" {
			a.AccountID = text(tokens, "account_id", "accountId")
		}
		if a.AccountID == "" {
			a.AccountID = tokenID
		}
		if tokenID != "" && a.AccountID != tokenID {
			return nil, 0, fmt.Errorf("identity_mismatch_row_%d", i+1)
		}
		tokenEmail := text(id, "email")
		if tokenEmail == "" {
			tokenEmail = text(access, "email")
		}
		if tokenEmail == "" {
			tokenEmail = text(object(access["https://api.openai.com/profile"]), "email")
		}
		if a.Email == "" {
			a.Email = strings.ToLower(tokenEmail)
		}
		if tokenEmail != "" && !strings.EqualFold(a.Email, tokenEmail) {
			return nil, 0, fmt.Errorf("identity_mismatch_row_%d", i+1)
		}
		user := text(auth, "chatgpt_user_id", "user_id")
		if a.UserID != "" && user != "" && a.UserID != user {
			return nil, 0, fmt.Errorf("identity_mismatch_row_%d", i+1)
		}
		if a.UserID == "" {
			a.UserID = user
		}
		if a.PlanType == "" {
			a.PlanType = text(auth, "chatgpt_plan_type", "plan_type")
		}
		if !validAccount(a) || access == nil || id == nil {
			return nil, 0, fmt.Errorf("invalid_account_row_%d", i+1)
		}
		key := identity(a)
		if index, ok := seen[key]; ok {
			result[index] = a
			duplicates++
		} else {
			seen[key] = len(result)
			result = append(result, a)
		}
	}
	return result, duplicates, nil
}
