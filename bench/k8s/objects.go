package k8s

// The Kubernetes object fields this package writes or reads, nothing more.
// JSON names follow the Kubernetes API (camelCase), not the bench's own
// snake_case: these structs are wire types of a foreign API.

// ObjectMeta is metav1.ObjectMeta, trimmed.
type ObjectMeta struct {
	Name              string            `json:"name,omitempty"`
	Namespace         string            `json:"namespace,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	UID               string            `json:"uid,omitempty"`
	OwnerReferences   []OwnerReference  `json:"ownerReferences,omitempty"`
	DeletionTimestamp string            `json:"deletionTimestamp,omitempty"`
	CreationTimestamp string            `json:"creationTimestamp,omitempty"`
}

// OwnerReference makes an object a dependent of another, so deleting the
// owner deletes it (garbage collection).
type OwnerReference struct {
	APIVersion         string `json:"apiVersion"`
	Kind               string `json:"kind"`
	Name               string `json:"name"`
	UID                string `json:"uid"`
	BlockOwnerDeletion *bool  `json:"blockOwnerDeletion,omitempty"`
}

// Namespace is corev1.Namespace.
type Namespace struct {
	APIVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Metadata   ObjectMeta `json:"metadata"`
	Status     struct {
		Phase string `json:"phase,omitempty"`
	} `json:"status,omitzero"`
}

// ConfigMap is corev1.ConfigMap.
type ConfigMap struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   ObjectMeta        `json:"metadata"`
	Data       map[string]string `json:"data,omitempty"`
	BinaryData map[string][]byte `json:"binaryData,omitempty"`
	Immutable  *bool             `json:"immutable,omitempty"`
}

// Secret is corev1.Secret. Data values are base64 on the wire, which is how
// encoding/json writes []byte.
type Secret struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   ObjectMeta        `json:"metadata"`
	Type       string            `json:"type,omitempty"`
	Data       map[string][]byte `json:"data,omitempty"`
	Immutable  *bool             `json:"immutable,omitempty"`
}

// Pod is corev1.Pod.
type Pod struct {
	APIVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       PodSpec    `json:"spec"`
	Status     PodStatus  `json:"status,omitzero"`
}

// PodSpec is corev1.PodSpec, trimmed.
type PodSpec struct {
	RestartPolicy                 string              `json:"restartPolicy,omitempty"`
	ShareProcessNamespace         *bool               `json:"shareProcessNamespace,omitempty"`
	AutomountServiceAccountToken  *bool               `json:"automountServiceAccountToken,omitempty"`
	ServiceAccountName            string              `json:"serviceAccountName,omitempty"`
	EnableServiceLinks            *bool               `json:"enableServiceLinks,omitempty"`
	HostAliases                   []HostAlias         `json:"hostAliases,omitempty"`
	DNSPolicy                     string              `json:"dnsPolicy,omitempty"`
	DNSConfig                     *PodDNSConfig       `json:"dnsConfig,omitempty"`
	InitContainers                []Container         `json:"initContainers,omitempty"`
	Containers                    []Container         `json:"containers"`
	Volumes                       []Volume            `json:"volumes,omitempty"`
	NodeSelector                  map[string]string   `json:"nodeSelector,omitempty"`
	Tolerations                   []Toleration        `json:"tolerations,omitempty"`
	SecurityContext               *PodSecurityContext `json:"securityContext,omitempty"`
	TerminationGracePeriodSeconds *int64              `json:"terminationGracePeriodSeconds,omitempty"`
	ActiveDeadlineSeconds         *int64              `json:"activeDeadlineSeconds,omitempty"`
	NodeName                      string              `json:"nodeName,omitempty"`
	HostNetwork                   bool                `json:"hostNetwork,omitempty"`
	HostPID                       bool                `json:"hostPID,omitempty"`
	HostIPC                       bool                `json:"hostIPC,omitempty"`
}

// HostAlias is one /etc/hosts entry.
type HostAlias struct {
	IP        string   `json:"ip"`
	Hostnames []string `json:"hostnames"`
}

// PodDNSConfig replaces the pod's resolv.conf when DNSPolicy is None.
type PodDNSConfig struct {
	Nameservers []string `json:"nameservers,omitempty"`
	Searches    []string `json:"searches,omitempty"`
}

// Toleration is corev1.Toleration.
type Toleration struct {
	Key               string `json:"key,omitempty" yaml:"key"`
	Operator          string `json:"operator,omitempty" yaml:"operator"`
	Value             string `json:"value,omitempty" yaml:"value"`
	Effect            string `json:"effect,omitempty" yaml:"effect"`
	TolerationSeconds *int64 `json:"tolerationSeconds,omitempty" yaml:"toleration_seconds"`
}

// PodSecurityContext is the pod-level security context.
type PodSecurityContext struct {
	RunAsNonRoot   *bool           `json:"runAsNonRoot,omitempty"`
	SeccompProfile *SeccompProfile `json:"seccompProfile,omitempty"`
}

// SeccompProfile selects a seccomp profile.
type SeccompProfile struct {
	Type string `json:"type"`
}

// Container is corev1.Container, trimmed. RestartPolicy Always on an init
// container makes it a native sidecar (Kubernetes 1.29 and later).
type Container struct {
	Name                     string                `json:"name"`
	Image                    string                `json:"image"`
	ImagePullPolicy          string                `json:"imagePullPolicy,omitempty"`
	Command                  []string              `json:"command,omitempty"`
	Args                     []string              `json:"args,omitempty"`
	WorkingDir               string                `json:"workingDir,omitempty"`
	Env                      []EnvVar              `json:"env,omitempty"`
	EnvFrom                  []EnvFromSource       `json:"envFrom,omitempty"`
	VolumeMounts             []VolumeMount         `json:"volumeMounts,omitempty"`
	Resources                *ResourceRequirements `json:"resources,omitempty"`
	SecurityContext          *SecurityContext      `json:"securityContext,omitempty"`
	Stdin                    bool                  `json:"stdin,omitempty"`
	StdinOnce                bool                  `json:"stdinOnce,omitempty"`
	RestartPolicy            string                `json:"restartPolicy,omitempty"`
	StartupProbe             *Probe                `json:"startupProbe,omitempty"`
	ReadinessProbe           *Probe                `json:"readinessProbe,omitempty"`
	Ports                    []ContainerPort       `json:"ports,omitempty"`
	TerminationMessagePolicy string                `json:"terminationMessagePolicy,omitempty"`
}

// ContainerPort is one port a container listens on.
type ContainerPort struct {
	Name          string `json:"name,omitempty"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol,omitempty"`
}

// EnvVar is one plain environment variable.
type EnvVar struct {
	Name      string        `json:"name"`
	Value     string        `json:"value,omitempty"`
	ValueFrom *EnvVarSource `json:"valueFrom,omitempty"`
}

// EnvVarSource reads one variable from a secret key.
type EnvVarSource struct {
	SecretKeyRef *KeySelector `json:"secretKeyRef,omitempty"`
}

// KeySelector names one key of a secret.
type KeySelector struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// EnvFromSource loads every key of a secret as a variable.
type EnvFromSource struct {
	SecretRef *LocalObjectReference `json:"secretRef,omitempty"`
}

// LocalObjectReference names an object in the same namespace.
type LocalObjectReference struct {
	Name string `json:"name"`
}

// VolumeMount mounts a pod volume into a container.
type VolumeMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
	SubPath   string `json:"subPath,omitempty"`
}

// ResourceRequirements are requests and limits, as quantity strings.
type ResourceRequirements struct {
	Requests map[string]string `json:"requests,omitempty"`
	Limits   map[string]string `json:"limits,omitempty"`
}

// SecurityContext is the container-level security context.
type SecurityContext struct {
	RunAsUser                *int64        `json:"runAsUser,omitempty"`
	RunAsGroup               *int64        `json:"runAsGroup,omitempty"`
	RunAsNonRoot             *bool         `json:"runAsNonRoot,omitempty"`
	Privileged               *bool         `json:"privileged,omitempty"`
	AllowPrivilegeEscalation *bool         `json:"allowPrivilegeEscalation,omitempty"`
	ReadOnlyRootFilesystem   *bool         `json:"readOnlyRootFilesystem,omitempty"`
	Capabilities             *Capabilities `json:"capabilities,omitempty"`
}

// Capabilities adds and drops Linux capabilities.
type Capabilities struct {
	Add  []string `json:"add,omitempty"`
	Drop []string `json:"drop,omitempty"`
}

// Probe is an exec or HTTP probe.
type Probe struct {
	Exec             *ExecAction    `json:"exec,omitempty"`
	HTTPGet          *HTTPGetAction `json:"httpGet,omitempty"`
	PeriodSeconds    int            `json:"periodSeconds,omitempty"`
	FailureThreshold int            `json:"failureThreshold,omitempty"`
	TimeoutSeconds   int            `json:"timeoutSeconds,omitempty"`
}

// ExecAction runs a command in the container.
type ExecAction struct {
	Command []string `json:"command"`
}

// HTTPGetAction probes an HTTP path.
type HTTPGetAction struct {
	Path string `json:"path"`
	Port int    `json:"port"`
}

// Volume is a pod volume: an emptyDir, a config map or a secret. No host
// path type exists here on purpose: the driver never mounts a node folder.
type Volume struct {
	Name      string                 `json:"name"`
	EmptyDir  *EmptyDirVolumeSource  `json:"emptyDir,omitempty"`
	ConfigMap *ConfigMapVolumeSource `json:"configMap,omitempty"`
	Secret    *SecretVolumeSource    `json:"secret,omitempty"`
}

// EmptyDirVolumeSource is a per-pod scratch folder; Medium Memory is tmpfs.
type EmptyDirVolumeSource struct {
	Medium    string `json:"medium,omitempty"`
	SizeLimit string `json:"sizeLimit,omitempty"`
}

// ConfigMapVolumeSource mounts a config map's keys as files.
type ConfigMapVolumeSource struct {
	Name        string `json:"name"`
	DefaultMode *int32 `json:"defaultMode,omitempty"`
}

// SecretVolumeSource mounts a secret's keys as files.
type SecretVolumeSource struct {
	SecretName  string `json:"secretName"`
	DefaultMode *int32 `json:"defaultMode,omitempty"`
}

// PodStatus is the part of the pod status the driver reads.
type PodStatus struct {
	Phase                 string            `json:"phase,omitempty"`
	Reason                string            `json:"reason,omitempty"`
	Message               string            `json:"message,omitempty"`
	PodIP                 string            `json:"podIP,omitempty"`
	InitContainerStatuses []ContainerStatus `json:"initContainerStatuses,omitempty"`
	ContainerStatuses     []ContainerStatus `json:"containerStatuses,omitempty"`
	Conditions            []struct {
		Type    string `json:"type"`
		Status  string `json:"status"`
		Reason  string `json:"reason,omitempty"`
		Message string `json:"message,omitempty"`
	} `json:"conditions,omitempty"`
}

// ContainerStatus is one container's state.
type ContainerStatus struct {
	Name         string         `json:"name"`
	Ready        bool           `json:"ready"`
	Started      *bool          `json:"started,omitempty"`
	RestartCount int            `json:"restartCount"`
	Image        string         `json:"image,omitempty"`
	ImageID      string         `json:"imageID,omitempty"`
	State        ContainerState `json:"state"`
}

// ContainerState is one of waiting, running or terminated.
type ContainerState struct {
	Waiting *struct {
		Reason  string `json:"reason,omitempty"`
		Message string `json:"message,omitempty"`
	} `json:"waiting,omitempty"`
	Running *struct {
		StartedAt string `json:"startedAt,omitempty"`
	} `json:"running,omitempty"`
	Terminated *struct {
		ExitCode   int    `json:"exitCode"`
		Signal     int    `json:"signal,omitempty"`
		Reason     string `json:"reason,omitempty"`
		Message    string `json:"message,omitempty"`
		StartedAt  string `json:"startedAt,omitempty"`
		FinishedAt string `json:"finishedAt,omitempty"`
	} `json:"terminated,omitempty"`
}

// NetworkPolicy is networkingv1.NetworkPolicy.
type NetworkPolicy struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   ObjectMeta        `json:"metadata"`
	Spec       NetworkPolicySpec `json:"spec"`
}

// NetworkPolicySpec selects pods and lists what they may send and receive.
// Empty Ingress and Egress lists with both policy types deny everything.
type NetworkPolicySpec struct {
	PodSelector LabelSelector       `json:"podSelector"`
	PolicyTypes []string            `json:"policyTypes"`
	Ingress     []NetworkPolicyRule `json:"ingress,omitempty"`
	Egress      []NetworkPolicyRule `json:"egress,omitempty"`
}

// NetworkPolicyRule is one ingress or egress rule; peers go in To (egress)
// or From (ingress).
type NetworkPolicyRule struct {
	To    []NetworkPolicyPeer `json:"to,omitempty"`
	From  []NetworkPolicyPeer `json:"from,omitempty"`
	Ports []NetworkPolicyPort `json:"ports,omitempty"`
}

// NetworkPolicyPeer selects pods, optionally in other namespaces.
type NetworkPolicyPeer struct {
	PodSelector       *LabelSelector `json:"podSelector,omitempty"`
	NamespaceSelector *LabelSelector `json:"namespaceSelector,omitempty"`
}

// NetworkPolicyPort is a protocol and port.
type NetworkPolicyPort struct {
	Protocol string `json:"protocol,omitempty"`
	Port     int    `json:"port,omitempty"`
}

// LabelSelector matches labels exactly. An empty selector selects all.
type LabelSelector struct {
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
}

// Service is corev1.Service.
type Service struct {
	APIVersion string      `json:"apiVersion"`
	Kind       string      `json:"kind"`
	Metadata   ObjectMeta  `json:"metadata"`
	Spec       ServiceSpec `json:"spec"`
}

// ServiceSpec is the part of a service spec the driver sets or reads.
type ServiceSpec struct {
	Type      string            `json:"type,omitempty"`
	Selector  map[string]string `json:"selector,omitempty"`
	Ports     []ServicePort     `json:"ports"`
	ClusterIP string            `json:"clusterIP,omitempty"`
}

// ServicePort maps a service port to a pod port.
type ServicePort struct {
	Name       string `json:"name,omitempty"`
	Port       int    `json:"port"`
	TargetPort int    `json:"targetPort,omitempty"`
	NodePort   int    `json:"nodePort,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
}

// Deployment is appsv1.Deployment.
type Deployment struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   ObjectMeta     `json:"metadata"`
	Spec       DeploymentSpec `json:"spec"`
	Status     struct {
		ReadyReplicas     int `json:"readyReplicas,omitempty"`
		AvailableReplicas int `json:"availableReplicas,omitempty"`
	} `json:"status,omitzero"`
}

// DeploymentSpec runs replicas of one pod template.
type DeploymentSpec struct {
	Replicas int           `json:"replicas"`
	Selector LabelSelector `json:"selector"`
	Template struct {
		Metadata ObjectMeta `json:"metadata"`
		Spec     PodSpec    `json:"spec"`
	} `json:"template"`
}

// Event is corev1.Event, the fields the driver reports.
type Event struct {
	Type           string `json:"type"`
	Reason         string `json:"reason"`
	Message        string `json:"message"`
	Count          int    `json:"count,omitempty"`
	FirstTimestamp string `json:"firstTimestamp,omitempty"`
	LastTimestamp  string `json:"lastTimestamp,omitempty"`
	InvolvedObject struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		FieldPath string `json:"fieldPath,omitempty"`
	} `json:"involvedObject"`
}

// Node is the part of corev1.Node the image check reads.
type Node struct {
	Metadata ObjectMeta `json:"metadata"`
	Status   struct {
		Images []struct {
			Names     []string `json:"names"`
			SizeBytes int64    `json:"sizeBytes"`
		} `json:"images"`
		NodeInfo struct {
			KubeletVersion          string `json:"kubeletVersion"`
			Architecture            string `json:"architecture"`
			ContainerRuntimeVersion string `json:"containerRuntimeVersion"`
		} `json:"nodeInfo"`
	} `json:"status"`
}

func boolPtr(b bool) *bool    { return &b }
func int64Ptr(n int64) *int64 { return &n }
func int32Ptr(n int32) *int32 { return &n }
