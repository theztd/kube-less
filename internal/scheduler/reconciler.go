package scheduler

// Action represents the reconciliation action to take for a workload.
type Action int

const (
	ActionNone     Action = iota // desired == actual, nothing to do
	ActionCreate                 // sandbox does not exist, create it
	ActionRecreate               // sandbox exists but config hash changed, recreate
	ActionDelete                 // workload removed from store, delete sandbox
)

// String returns a human-readable name of the action.
func (a Action) String() string {
	switch a {
	case ActionCreate:
		return "Create"
	case ActionRecreate:
		return "Recreate"
	case ActionDelete:
		return "Delete"
	default:
		return "None"
	}
}

// compare determines what action is needed for a workload.
//
//   - desired == nil                              → ActionDelete
//   - desired != nil, no running sandbox          → ActionCreate
//   - desired != nil, sandbox exists, hash match  → ActionNone
//   - desired != nil, sandbox exists, hash differ → ActionRecreate
func compare(desired *WorkloadState, actualSandboxID, actualConfigHash string) Action {
	if desired == nil {
		return ActionDelete
	}
	if actualSandboxID == "" {
		return ActionCreate
	}
	if desired.ConfigHash != actualConfigHash {
		return ActionRecreate
	}
	return ActionNone
}
