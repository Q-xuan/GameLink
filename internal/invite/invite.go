// Package invite formats and parses the one string a host copies to a guest.
// The canonical form is gamelink://join/<code>/<token>.
package invite

import (
	"errors"
	"net/url"
	"strings"

	"github.com/Q-xuan/GameLink/internal/room"
)

// Format builds the invite string shown after a room is created.
func Format(code, token string) string {
	return "gamelink://join/" + url.PathEscape(code) + "/" + url.PathEscape(token)
}

// IsURL reports whether s is a gamelink:// link.
func IsURL(s string) bool {
	s = trimInvite(s)
	return strings.HasPrefix(strings.ToLower(s), "gamelink://")
}

// Parse accepts the invite string, a gamelink:// link, or "<code> <token>".
func Parse(raw string) (code, token string, err error) {
	s := trimInvite(raw)
	if s == "" {
		return "", "", errors.New("邀请串是空的")
	}
	if strings.Contains(s, "://") {
		return parseURL(s)
	}
	fields := strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', '/', ',', '|':
			return true
		default:
			return false
		}
	})
	if len(fields) < 2 {
		return "", "", errors.New("邀请串里需要房间码和令牌")
	}
	return normalize(fields[0], fields[1])
}

func parseURL(s string) (string, string, error) {
	u, err := url.Parse(s)
	if err != nil || !strings.EqualFold(u.Scheme, "gamelink") {
		return "", "", errors.New("无法识别邀请链接")
	}
	var parts []string
	if u.Host != "" {
		parts = append(parts, u.Host)
	}
	if u.Opaque != "" {
		parts = append(parts, strings.Split(strings.Trim(u.Opaque, "/"), "/")...)
	}
	if u.Path != "" {
		parts = append(parts, strings.Split(strings.Trim(u.Path, "/"), "/")...)
	}
	if len(parts) >= 3 && strings.EqualFold(parts[0], "join") {
		return normalize(parts[1], parts[2])
	}
	if len(parts) >= 2 {
		return normalize(parts[len(parts)-2], parts[len(parts)-1])
	}
	return "", "", errors.New("邀请链接里需要房间码和令牌")
}

func normalize(code, token string) (string, string, error) {
	code, err := room.NormalizeCode(code)
	if err != nil {
		return "", "", errors.New("房间码无效")
	}
	token = strings.TrimSpace(token)
	if _, err := room.ParseToken(token); err != nil {
		return "", "", errors.New("令牌无效")
	}
	return code, strings.ToLower(token), nil
}

func trimInvite(s string) string {
	s = strings.TrimSpace(s)
	return strings.Trim(s, `"'`)
}
