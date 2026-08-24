// Package ids generates sortable, high-entropy, prefixed identifiers.
//
// The encoding is ULID (48-bit millisecond timestamp + 80 bits of
// randomness, Crockford base32) so identifiers sort lexicographically by
// creation time, which keeps btree indexes on them well behaved.
package ids

import (
	"crypto/rand"
	"errors"
	"strings"
	"time"
)

// Prefix namespaces an identifier by the kind of entity it names.
type Prefix string

const (
	Application    Prefix = "app"
	APIKey         Prefix = "key"
	Payment        Prefix = "pay"
	Order          Prefix = "pmx"
	Transaction    Prefix = "txn"
	Event          Prefix = "evt"
	Delivery       Prefix = "dlv"
	Attempt        Prefix = "att"
	Destination    Prefix = "dst"
	Subscription   Prefix = "sub"
	Refund         Prefix = "rfd"
	Payout         Prefix = "pyo"
	Beneficiary    Prefix = "ben"
	Transition     Prefix = "trn"
	GatewayAccount Prefix = "gwa"
	GatewayEvent   Prefix = "gev"
	Admin          Prefix = "adm"
	Session        Prefix = "ses"
	Request        Prefix = "req"
	Job            Prefix = "job"
	AuditLog       Prefix = "aud"
)

const encoding = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var decoding = func() [256]byte {
	var t [256]byte
	for i := range t {
		t[i] = 0xFF
	}
	for i, c := range []byte(encoding) {
		t[c] = byte(i)
		if c >= 'A' && c <= 'Z' {
			t[c+32] = byte(i) // accept lowercase
		}
	}
	// Crockford aliases.
	t['o'], t['O'] = 0, 0
	t['i'], t['I'], t['l'], t['L'] = 1, 1, 1, 1
	return t
}()

// New returns a new identifier of the form "<prefix>_<ulid>".
func New(p Prefix) string {
	return string(p) + "_" + newULID(time.Now())
}

// ErrInvalidID reports an identifier that is not well formed.
var ErrInvalidID = errors.New("invalid identifier")

// Validate reports whether id is a well-formed identifier carrying prefix p.
func Validate(p Prefix, id string) error {
	want := string(p) + "_"
	if !strings.HasPrefix(id, want) {
		return ErrInvalidID
	}
	body := id[len(want):]
	if len(body) != 26 {
		return ErrInvalidID
	}
	for i := 0; i < len(body); i++ {
		if decoding[body[i]] == 0xFF {
			return ErrInvalidID
		}
	}
	return nil
}

// PrefixOf returns the prefix portion of id, or "" when id is not prefixed.
func PrefixOf(id string) string {
	if i := strings.IndexByte(id, '_'); i > 0 {
		return id[:i]
	}
	return ""
}

// Time extracts the creation timestamp encoded in id.
func Time(id string) (time.Time, error) {
	i := strings.IndexByte(id, '_')
	if i < 0 || len(id)-i-1 != 26 {
		return time.Time{}, ErrInvalidID
	}
	body := id[i+1:]
	var ms uint64
	for j := 0; j < 10; j++ { // first 10 chars carry the 48-bit timestamp
		v := decoding[body[j]]
		if v == 0xFF {
			return time.Time{}, ErrInvalidID
		}
		ms = ms<<5 | uint64(v)
	}
	return time.UnixMilli(int64(ms)).UTC(), nil
}

func newULID(t time.Time) string {
	var b [16]byte
	ms := uint64(t.UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	if _, err := rand.Read(b[6:]); err != nil {
		panic("ids: entropy source failed: " + err.Error())
	}
	return encodeULID(b)
}

// encodeULID renders the 16 bytes as 26 Crockford base32 characters.
func encodeULID(b [16]byte) string {
	out := make([]byte, 26)
	out[0] = encoding[(b[0]&224)>>5]
	out[1] = encoding[b[0]&31]
	out[2] = encoding[(b[1]&248)>>3]
	out[3] = encoding[((b[1]&7)<<2)|((b[2]&192)>>6)]
	out[4] = encoding[(b[2]&62)>>1]
	out[5] = encoding[((b[2]&1)<<4)|((b[3]&240)>>4)]
	out[6] = encoding[((b[3]&15)<<1)|((b[4]&128)>>7)]
	out[7] = encoding[(b[4]&124)>>2]
	out[8] = encoding[((b[4]&3)<<3)|((b[5]&224)>>5)]
	out[9] = encoding[b[5]&31]
	out[10] = encoding[(b[6]&248)>>3]
	out[11] = encoding[((b[6]&7)<<2)|((b[7]&192)>>6)]
	out[12] = encoding[(b[7]&62)>>1]
	out[13] = encoding[((b[7]&1)<<4)|((b[8]&240)>>4)]
	out[14] = encoding[((b[8]&15)<<1)|((b[9]&128)>>7)]
	out[15] = encoding[(b[9]&124)>>2]
	out[16] = encoding[((b[9]&3)<<3)|((b[10]&224)>>5)]
	out[17] = encoding[b[10]&31]
	out[18] = encoding[(b[11]&248)>>3]
	out[19] = encoding[((b[11]&7)<<2)|((b[12]&192)>>6)]
	out[20] = encoding[(b[12]&62)>>1]
	out[21] = encoding[((b[12]&1)<<4)|((b[13]&240)>>4)]
	out[22] = encoding[((b[13]&15)<<1)|((b[14]&128)>>7)]
	out[23] = encoding[(b[14]&124)>>2]
	out[24] = encoding[((b[14]&3)<<3)|((b[15]&224)>>5)]
	out[25] = encoding[b[15]&31]
	return string(out)
}
