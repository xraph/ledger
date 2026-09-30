package extension

import (
	dashboard "github.com/xraph/forge/extensions/dashboard"
)

// The dashboard discovers ledger's contract contributor at runtime, so the
// production package never imports forge's dashboard root. This keeps the
// method signature checked against the real interface anyway.
var _ dashboard.ContractContributorAware = (*Extension)(nil)
