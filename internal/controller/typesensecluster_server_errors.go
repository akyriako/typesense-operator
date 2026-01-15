package controller

import "github.com/pkg/errors"

var (
	ErrFailedToStartPeeringState         = errors.New("Failed to start peering state")
	ErrCannotTruncateLogsBeforeAppliedID = errors.New("Can't truncate logs before _applied_id")
)

var (
	ErrorsRequirePodTermination = []error{ErrFailedToStartPeeringState, ErrCannotTruncateLogsBeforeAppliedID}
)
