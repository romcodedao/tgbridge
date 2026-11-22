package handoff

import (
	"github.com/romcodedao/tgbridge/internal/bot"
	"github.com/romcodedao/tgbridge/internal/orca"
)

var (
	_ bot.HandoffAPI = (*Manager)(nil)
	_ bot.OrcaAPI    = (*orca.CLI)(nil)
	_ Orca           = (*orca.CLI)(nil)
	_ AskFunc        = (*bot.Bot)(nil).Ask
)
