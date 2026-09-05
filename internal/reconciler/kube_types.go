package reconciler

type objectMeta struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	UID               string            `json:"uid"`
	DeletionTimestamp *string           `json:"deletionTimestamp"`
	Labels            map[string]string `json:"labels"`
	Continue          string            `json:"continue"`
}

type Node struct {
	Kind     string     `json:"kind"`
	Metadata objectMeta `json:"metadata"`
}

type containerState struct {
	Running *struct{} `json:"running"`
}

type ContainerStatus struct {
	Name        string         `json:"name"`
	ContainerID string         `json:"containerID"`
	State       containerState `json:"state"`
}

type Pod struct {
	Kind     string     `json:"kind"`
	Metadata objectMeta `json:"metadata"`
	Spec     struct {
		NodeName string `json:"nodeName"`
	} `json:"spec"`
	Status struct {
		Phase             string            `json:"phase"`
		QoSClass          string            `json:"qosClass"`
		ContainerStatuses []ContainerStatus `json:"containerStatuses"`
	} `json:"status"`
}

type PodList struct {
	Kind     string     `json:"kind"`
	Metadata objectMeta `json:"metadata"`
	Items    []Pod      `json:"items"`
}

type Candidate struct {
	Namespace     string
	PodName       string
	PodUID        string
	ContainerName string
	ContainerID   string
	QoSClass      string
}
