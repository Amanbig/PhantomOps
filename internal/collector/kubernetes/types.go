package kubernetes

// Minimal projections of the Kubernetes API objects we care about.

type objectMeta struct {
	Name              string `json:"name"`
	Namespace         string `json:"namespace"`
	CreationTimestamp string `json:"creationTimestamp"`
}

type containerStatus struct {
	Name         string `json:"name"`
	Ready        bool   `json:"ready"`
	RestartCount int    `json:"restartCount"`
	State        struct {
		Waiting *struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"waiting"`
		Running *struct {
			StartedAt string `json:"startedAt"`
		} `json:"running"`
	} `json:"state"`
	LastState struct {
		Terminated *struct {
			Reason     string `json:"reason"`
			ExitCode   int    `json:"exitCode"`
			FinishedAt string `json:"finishedAt"`
		} `json:"terminated"`
	} `json:"lastState"`
}

type pod struct {
	Metadata objectMeta `json:"metadata"`
	Spec     struct {
		Containers []struct {
			Name      string `json:"name"`
			Resources struct {
				Limits map[string]string `json:"limits"`
			} `json:"resources"`
		} `json:"containers"`
	} `json:"spec"`
	Status struct {
		Phase             string            `json:"phase"`
		Reason            string            `json:"reason"`
		Message           string            `json:"message"`
		StartTime         string            `json:"startTime"`
		ContainerStatuses []containerStatus `json:"containerStatuses"`
	} `json:"status"`
}

type podList struct {
	Items []pod `json:"items"`
}

type event struct {
	Metadata       objectMeta `json:"metadata"`
	Type           string     `json:"type"`
	Reason         string     `json:"reason"`
	Message        string     `json:"message"`
	Count          int        `json:"count"`
	LastTimestamp  string     `json:"lastTimestamp"`
	EventTime      string     `json:"eventTime"`
	InvolvedObject struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"involvedObject"`
}

type eventList struct {
	Items []event `json:"items"`
}

type deployment struct {
	Metadata objectMeta `json:"metadata"`
	Spec     struct {
		Replicas int `json:"replicas"`
	} `json:"spec"`
	Status struct {
		UnavailableReplicas int `json:"unavailableReplicas"`
		Conditions          []struct {
			Type           string `json:"type"`
			Status         string `json:"status"`
			Reason         string `json:"reason"`
			Message        string `json:"message"`
			LastUpdateTime string `json:"lastUpdateTime"`
		} `json:"conditions"`
	} `json:"status"`
}

type deploymentList struct {
	Items []deployment `json:"items"`
}

type podMetrics struct {
	Metadata   objectMeta `json:"metadata"`
	Containers []struct {
		Name  string `json:"name"`
		Usage struct {
			Memory string `json:"memory"`
		} `json:"usage"`
	} `json:"containers"`
}

type podMetricsList struct {
	Items []podMetrics `json:"items"`
}
