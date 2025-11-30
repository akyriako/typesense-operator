package v1alpha1

type MaintenanceSpec struct {
	// +kubebuilder:default=true
	// +kubebuilder:validation:Type=boolean
	Enabled bool `json:"enabled"`

	// +kubebuilder:default=true
	// +kubebuilder:validation:Type=boolean
	ScaleDown *bool `json:"scaleDown,omitempty"`

	// +kubebuilder:validation:Optional
	Schedule *MaintenanceScheduleSpec `json:"schedule,omitempty"`
}

type MaintenanceScheduleSpec struct {
	// +kubebuilder:validation:Pattern:=`(^((\*\/)?([0-5]?[0-9])((\,|\-|\/)([0-5]?[0-9]))*|\*)\s+((\*\/)?((2[0-3]|1[0-9]|[0-9]|00))((\,|\-|\/)(2[0-3]|1[0-9]|[0-9]|00))*|\*)\s+((\*\/)?([1-9]|[12][0-9]|3[01])((\,|\-|\/)([1-9]|[12][0-9]|3[01]))*|\*)\s+((\*\/)?([1-9]|1[0-2])((\,|\-|\/)([1-9]|1[0-2]))*|\*|(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|des))\s+((\*\/)?[0-6]((\,|\-|\/)[0-6])*|\*|00|(sun|mon|tue|wed|thu|fri|sat))\s*$)|@(annually|yearly|monthly|weekly|daily|hourly|reboot)`
	// +kubebuilder:validation:Type=string
	CronExpression *string `json:"cronExpression,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:="UTC"
	Timezone *string `json:"timezone,omitempty"`

	// +kubebuilder:validation:Type=string
	ExecHook *string `json:"execHook,omitempty"`

	// +kubebuilder:default=false
	// +kubebuilder:validation:Type=boolean
	OnExecHookErrorContinue *bool `json:"onExecHookErrorContinue,omitempty"`
}
