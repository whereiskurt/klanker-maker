package slack

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// MaxExclusiveReactions caps the --exclusive set on a react envelope. Each
// name costs one reactions.remove call at the bridge, so the cap bounds how
// many Slack calls a single signed request can fan out into. Checked by the
// CLI before signing and again by the bridge before any call.
const MaxExclusiveReactions = 10

// reEmojiName is the shape Slack accepts for a reaction name: lowercase
// letters, digits, underscore, plus, apostrophe and hyphen, 1..100 chars.
// Skin-tone suffixes (`name::skin-tone-2`) are deliberately outside it.
var reEmojiName = regexp.MustCompile(`^[a-z0-9_+'-]{1,100}$`)

// ErrBadEmojiName is wrapped by NormalizeEmojiName for every rejection.
var ErrBadEmojiName = errors.New("slack: bad emoji name")

// NormalizeEmojiName strips the surrounding colons Slack renders a reaction
// with (`:white_check_mark:` → `white_check_mark`) and validates the bare name
// against reEmojiName. The same function runs on both sides of the bridge so
// a client that is bypassed cannot sign a name the bridge would then forward.
func NormalizeEmojiName(name string) (string, error) {
	n := strings.TrimSuffix(strings.TrimPrefix(name, ":"), ":")
	if !reEmojiName.MatchString(n) {
		return "", fmt.Errorf("%w: %q (want ^[a-z0-9_+'-]{1,100}$, colons optional)", ErrBadEmojiName, name)
	}
	return n, nil
}
