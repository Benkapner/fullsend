package forge

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatSignOffTrailer(t *testing.T) {
	got := FormatSignOffTrailer("Alice Smith", "alice@example.com")
	assert.Equal(t, "Signed-off-by: Alice Smith <alice@example.com>", got)
}

func TestFormatSignOffTrailer_StripsNewlines(t *testing.T) {
	got := FormatSignOffTrailer("Evil\nUser", "evil@example.com\r")
	assert.Equal(t, "Signed-off-by: EvilUser <evil@example.com>", got)
}

func TestUserIdentity_SignOffTrailer(t *testing.T) {
	id := &UserIdentity{Name: "Test User", Email: "test@example.com"}
	assert.Equal(t, "Signed-off-by: Test User <test@example.com>", id.SignOffTrailer())
}
