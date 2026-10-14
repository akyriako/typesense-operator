package controller

import "errors"

var (
	ErrFailedToStartPeeringState         = errors.New("failed to start peering state")
	ErrCannotTruncateLogsBeforeAppliedID = errors.New("can't truncate logs before _applied_id")
)

var (
	ErrorsRequirePodTermination = []error{ErrFailedToStartPeeringState, ErrCannotTruncateLogsBeforeAppliedID}
)
