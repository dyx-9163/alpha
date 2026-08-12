package store

import "time"

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Role         string    `json:"role"`
	TokenVersion int       `json:"tokenVersion"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"createdAt"`
}

type UserSummary struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Role         string    `json:"role"`
	TokenVersion int       `json:"tokenVersion"`
	CreatedAt    time.Time `json:"createdAt"`
}

type Server struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Host       string    `json:"host"`
	Port       int       `json:"port"`
	Username   string    `json:"username"`
	AuthType   string    `json:"authType"`
	Password   string    `json:"-"`
	PrivateKey string    `json:"-"`
	Tags       string    `json:"tags"`
	Note       string    `json:"note"`
	DeployDir  string    `json:"deployDir"`
	DockerHost string    `json:"dockerHost"`
	Status     string    `json:"status"`
	LastError  string    `json:"lastError,omitempty"`
	SortOrder  int       `json:"sortOrder"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type Task struct {
	ID             string    `json:"id"`
	Type           string    `json:"type"`
	Category       string    `json:"category,omitempty"`
	Trackable      bool      `json:"trackable"`
	Target         string    `json:"target"`
	Status         string    `json:"status"`
	CreatedBy      string    `json:"createdBy"`
	Error          string    `json:"error,omitempty"`
	LeaseOwner     string    `json:"leaseOwner,omitempty"`
	LeaseExpiresAt time.Time `json:"leaseExpiresAt,omitempty"`
	Attempt        int       `json:"attempt,omitempty"`
	IdempotencyKey string    `json:"idempotencyKey,omitempty"`
	CorrelationID  string    `json:"correlationId,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	StartedAt      time.Time `json:"startedAt,omitempty"`
	FinishedAt     time.Time `json:"finishedAt,omitempty"`
}

type TaskLog struct {
	ID        int64     `json:"id"`
	TaskID    string    `json:"taskId"`
	Target    string    `json:"target,omitempty"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}

type TaskTarget struct {
	ID         int64     `json:"id"`
	TaskID     string    `json:"taskId"`
	Target     string    `json:"target"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
}

type TaskStep struct {
	ID         int64     `json:"id"`
	TaskID     string    `json:"taskId"`
	Target     string    `json:"target"`
	Name       string    `json:"name"`
	Title      string    `json:"title"`
	Order      int       `json:"order"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
}

type Audit struct {
	ID        int64     `json:"id"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}

type AuditQuery struct {
	Page     int
	PageSize int
	Module   string
	Status   string
}

type AuditPage struct {
	Items    []Audit `json:"items"`
	Total    int     `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"pageSize"`
}

type Resource struct {
	ID        string    `json:"id"`
	App       string    `json:"app"`
	Part      string    `json:"part"`
	Version   string    `json:"version"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256,omitempty"`
	RPMCount  int       `json:"rpmCount"`
	CreatedAt time.Time `json:"createdAt"`
}

type AppInstance struct {
	ID        string    `json:"id"`
	App       string    `json:"app"`
	Version   string    `json:"version"`
	ServerID  string    `json:"serverId"`
	Status    string    `json:"status"`
	Topology  string    `json:"topology"`
	Metadata  string    `json:"metadata"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type AppRelease struct {
	ID           string    `json:"id"`
	InstanceID   string    `json:"instanceId"`
	App          string    `json:"app"`
	Version      string    `json:"version"`
	ReleaseID    string    `json:"releaseId"`
	ServerID     string    `json:"serverId"`
	Status       string    `json:"status"`
	ManifestJSON string    `json:"manifestJson"`
	ConfigHash   string    `json:"configHash"`
	CreatedAt    time.Time `json:"createdAt"`
	ActivatedAt  time.Time `json:"activatedAt,omitempty"`
}

type AppReleaseArtifact struct {
	ID           string    `json:"id"`
	InstanceID   string    `json:"instanceId"`
	ReleaseID    string    `json:"releaseId"`
	App          string    `json:"app"`
	ServiceName  string    `json:"serviceName,omitempty"`
	ArtifactType string    `json:"artifactType"`
	Name         string    `json:"name"`
	Version      string    `json:"version,omitempty"`
	Checksum     string    `json:"checksum,omitempty"`
	Size         int64     `json:"size"`
	Path         string    `json:"path,omitempty"`
	Metadata     string    `json:"metadata,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

type AppReleaseSnapshot struct {
	ID           string    `json:"id"`
	InstanceID   string    `json:"instanceId"`
	ReleaseID    string    `json:"releaseId"`
	App          string    `json:"app"`
	SnapshotKind string    `json:"snapshotKind"`
	Status       string    `json:"status"`
	PayloadJSON  string    `json:"payloadJson"`
	Checksum     string    `json:"checksum,omitempty"`
	Metadata     string    `json:"metadata,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	RestoredAt   time.Time `json:"restoredAt,omitempty"`
}

type AppBackup struct {
	ID          string    `json:"id"`
	App         string    `json:"app"`
	InstanceID  string    `json:"instanceId,omitempty"`
	ServerID    string    `json:"serverId,omitempty"`
	BackupType  string    `json:"backupType"`
	Status      string    `json:"status"`
	Path        string    `json:"path,omitempty"`
	Checksum    string    `json:"checksum,omitempty"`
	Size        int64     `json:"size"`
	TaskID      string    `json:"taskId,omitempty"`
	Metadata    string    `json:"metadata,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	CompletedAt time.Time `json:"completedAt,omitempty"`
}

type DiagnosticExport struct {
	ID                  string    `json:"id"`
	TaskID              string    `json:"taskId,omitempty"`
	InstanceID          string    `json:"instanceId"`
	ServerID            string    `json:"serverId"`
	Status              string    `json:"status"`
	ServicesJSON        string    `json:"-"`
	Services            []string  `json:"services"`
	LocalDate           string    `json:"localDate,omitempty"`
	SinceAt             time.Time `json:"sinceAt"`
	UntilAt             time.Time `json:"untilAt"`
	StorageKind         string    `json:"storageKind"`
	StorageRelativePath string    `json:"-"`
	ReservedBytes       int64     `json:"-"`
	RemoteRelativePath  string    `json:"-"`
	ArchiveName         string    `json:"archiveName,omitempty"`
	ArchiveBytes        int64     `json:"archiveBytes"`
	UncompressedBytes   int64     `json:"uncompressedBytes"`
	SHA256              string    `json:"sha256,omitempty"`
	WarningCount        int       `json:"warningCount"`
	WarningsJSON        string    `json:"-"`
	Warnings            []string  `json:"warnings,omitempty"`
	ErrorText           string    `json:"error,omitempty"`
	CreatedBy           string    `json:"createdBy"`
	CreatedAt           time.Time `json:"createdAt"`
	ReadyAt             time.Time `json:"readyAt,omitempty"`
	ExpiresAt           time.Time `json:"expiresAt"`
	DownloadedAt        time.Time `json:"downloadedAt,omitempty"`
	DeletedAt           time.Time `json:"deletedAt,omitempty"`
	CleanupStatus       string    `json:"cleanupStatus"`
	CleanupError        string    `json:"cleanupError,omitempty"`
	CleanupAttemptedAt  time.Time `json:"cleanupAttemptedAt,omitempty"`
}

type DiagnosticExportPage struct {
	Items    []DiagnosticExport `json:"items"`
	Total    int                `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"pageSize"`
}

type AppCluster struct {
	ID        string    `json:"id"`
	App       string    `json:"app"`
	Name      string    `json:"name"`
	Topology  string    `json:"topology"`
	Status    string    `json:"status"`
	Metadata  string    `json:"metadata,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type AppClusterMember struct {
	ID         string    `json:"id"`
	ClusterID  string    `json:"clusterId"`
	InstanceID string    `json:"instanceId"`
	ServerID   string    `json:"serverId,omitempty"`
	Role       string    `json:"role,omitempty"`
	Status     string    `json:"status"`
	Metadata   string    `json:"metadata,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type OperationLock struct {
	ID          string    `json:"id"`
	Scope       string    `json:"scope"`
	ResourceID  string    `json:"resourceId"`
	Operation   string    `json:"operation"`
	OwnerTaskID string    `json:"ownerTaskId,omitempty"`
	Owner       string    `json:"owner,omitempty"`
	Status      string    `json:"status"`
	ExpiresAt   time.Time `json:"expiresAt"`
	HeartbeatAt time.Time `json:"heartbeatAt"`
	ReleasedAt  time.Time `json:"releasedAt,omitempty"`
	Metadata    string    `json:"metadata,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type AIFARDeployment struct {
	ID                 string    `json:"id"`
	InstanceID         string    `json:"instanceId"`
	ServiceName        string    `json:"serviceName"`
	DesiredReplicas    int       `json:"desiredReplicas"`
	CurrentRevision    string    `json:"currentRevision"`
	UpdatingRevision   string    `json:"updatingRevision,omitempty"`
	StrategyJSON       string    `json:"strategyJson,omitempty"`
	SpecJSON           string    `json:"specJson,omitempty"`
	Generation         int64     `json:"generation"`
	ObservedGeneration int64     `json:"observedGeneration"`
	ObservationEpoch   int64     `json:"observationEpoch"`
	Status             string    `json:"status"`
	MetadataJSON       string    `json:"metadataJson,omitempty"`
	ConditionsJSON     string    `json:"conditionsJson,omitempty"`
	LastTransitionAt   time.Time `json:"lastTransitionAt,omitempty"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type AIFARReplicaSet struct {
	ID           string    `json:"id"`
	InstanceID   string    `json:"instanceId"`
	ServiceName  string    `json:"serviceName"`
	Revision     string    `json:"revision"`
	Image        string    `json:"image"`
	ArtifactHash string    `json:"artifactHash,omitempty"`
	DesiredPods  int       `json:"desiredPods"`
	ReadyPods    int       `json:"readyPods"`
	Status       string    `json:"status"`
	MetadataJSON string    `json:"metadataJson,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type AIFARPod struct {
	ID            string    `json:"id"`
	InstanceID    string    `json:"instanceId"`
	ServiceName   string    `json:"serviceName"`
	Revision      string    `json:"revision"`
	PodID         string    `json:"podId"`
	ContainerName string    `json:"containerName"`
	Port          int       `json:"port"`
	Status        string    `json:"status"`
	Ready         bool      `json:"ready"`
	MetadataJSON  string    `json:"metadataJson,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type AIFARServiceEndpoint struct {
	ID            string    `json:"id"`
	InstanceID    string    `json:"instanceId"`
	ServiceName   string    `json:"serviceName"`
	PodID         string    `json:"podId"`
	ContainerName string    `json:"containerName"`
	Revision      string    `json:"revision"`
	Port          int       `json:"port"`
	State         string    `json:"state"`
	Ready         bool      `json:"ready"`
	MetadataJSON  string    `json:"metadataJson,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type AIFAROrchestrationLock struct {
	ID          string    `json:"id"`
	InstanceID  string    `json:"instanceId"`
	ServiceName string    `json:"serviceName,omitempty"`
	Operation   string    `json:"operation"`
	Actor       string    `json:"actor,omitempty"`
	TaskID      string    `json:"taskId,omitempty"`
	Status      string    `json:"status"`
	StartedAt   time.Time `json:"startedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
	ReleasedAt  time.Time `json:"releasedAt,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type NacosConfigRevision struct {
	ID              string    `json:"id"`
	NacosInstanceID string    `json:"nacosInstanceId"`
	Namespace       string    `json:"namespace"`
	Group           string    `json:"group"`
	DataID          string    `json:"dataId"`
	Content         string    `json:"content,omitempty"`
	ContentHash     string    `json:"contentHash"`
	Metadata        string    `json:"metadata,omitempty"`
	CreatedBy       string    `json:"createdBy,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	PublishedAt     time.Time `json:"publishedAt,omitempty"`
}

type NacosConfigRevisionQuery struct {
	NacosInstanceID string
	Namespace       string
	Group           string
	DataID          string
	Limit           int
}

type Credential struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Kind           string            `json:"kind"`
	Username       string            `json:"username,omitempty"`
	Endpoint       string            `json:"endpoint,omitempty"`
	Scope          string            `json:"scope"`
	Status         string            `json:"status"`
	App            string            `json:"app,omitempty"`
	ServerID       string            `json:"serverId,omitempty"`
	AppInstanceID  string            `json:"appInstanceId,omitempty"`
	Purpose        string            `json:"purpose,omitempty"`
	Tags           string            `json:"tags,omitempty"`
	HasSecret      bool              `json:"hasSecret"`
	SecretPreview  string            `json:"secretPreview,omitempty"`
	Secret         map[string]string `json:"-"`
	CurrentVersion int               `json:"currentVersion"`
	CreatedBy      string            `json:"createdBy,omitempty"`
	CreatedAt      time.Time         `json:"createdAt"`
	UpdatedAt      time.Time         `json:"updatedAt"`
}

type CredentialVersion struct {
	ID           string    `json:"id"`
	CredentialID string    `json:"credentialId"`
	Version      int       `json:"version"`
	CreatedBy    string    `json:"createdBy,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	RetiredAt    time.Time `json:"retiredAt,omitempty"`
}

type CredentialBinding struct {
	ID            string    `json:"id"`
	CredentialID  string    `json:"credentialId"`
	AppInstanceID string    `json:"appInstanceId"`
	Purpose       string    `json:"purpose"`
	ServiceName   string    `json:"serviceName,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

type CredentialReference struct {
	ID              string    `json:"id"`
	CredentialID    string    `json:"credentialId"`
	ResourceType    string    `json:"resourceType"`
	ResourceID      string    `json:"resourceId"`
	Purpose         string    `json:"purpose,omitempty"`
	Generated       bool      `json:"generated"`
	LifecyclePolicy string    `json:"lifecyclePolicy"`
	Metadata        string    `json:"metadata,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type CredentialQuery struct {
	Kind   string
	Status string
	Q      string
}

type StorageItem struct {
	ID         string    `json:"id"`
	InstanceID string    `json:"instanceId"`
	Kind       string    `json:"kind"`
	Name       string    `json:"name"`
	Policy     string    `json:"policy,omitempty"`
	AccessKey  string    `json:"accessKey,omitempty"`
	SecretKey  string    `json:"secretKey,omitempty"`
	Metadata   string    `json:"metadata,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type CollectorRun struct {
	Name       string    `json:"name"`
	Target     string    `json:"target,omitempty"`
	Status     string    `json:"status"`
	LastError  string    `json:"lastError,omitempty"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
	DurationMS int64     `json:"durationMs"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type StatusSnapshot struct {
	Scope       string    `json:"scope"`
	ResourceID  string    `json:"resourceId"`
	ServerID    string    `json:"serverId,omitempty"`
	Status      string    `json:"status"`
	Payload     string    `json:"payload"`
	LastError   string    `json:"lastError,omitempty"`
	Version     int64     `json:"version"`
	CollectedAt time.Time `json:"collectedAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type StatusSnapshotHistory struct {
	ID          int64     `json:"id"`
	Scope       string    `json:"scope"`
	ResourceID  string    `json:"resourceId"`
	ServerID    string    `json:"serverId,omitempty"`
	Status      string    `json:"status"`
	Payload     string    `json:"payload"`
	LastError   string    `json:"lastError,omitempty"`
	Version     int64     `json:"version"`
	CollectedAt time.Time `json:"collectedAt"`
	CreatedAt   time.Time `json:"createdAt"`
}

type Alert struct {
	ID                 string    `json:"id"`
	Fingerprint        string    `json:"fingerprint"`
	Severity           string    `json:"severity"`
	Scope              string    `json:"scope"`
	ResourceID         string    `json:"resourceId,omitempty"`
	ServerID           string    `json:"serverId,omitempty"`
	App                string    `json:"app,omitempty"`
	InstanceID         string    `json:"instanceId,omitempty"`
	Status             string    `json:"status"`
	Title              string    `json:"title"`
	Message            string    `json:"message,omitempty"`
	EvidenceJSON       string    `json:"evidenceJson,omitempty"`
	RequiredPermission string    `json:"requiredPermission,omitempty"`
	FirstSeenAt        time.Time `json:"firstSeenAt"`
	LastSeenAt         time.Time `json:"lastSeenAt"`
	ResolvedAt         time.Time `json:"resolvedAt,omitempty"`
	MutedUntil         time.Time `json:"mutedUntil,omitempty"`
	AcknowledgedBy     string    `json:"acknowledgedBy,omitempty"`
	AcknowledgedAt     time.Time `json:"acknowledgedAt,omitempty"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type AlertEvent struct {
	ID          int64     `json:"id"`
	AlertID     string    `json:"alertId"`
	Fingerprint string    `json:"fingerprint"`
	Event       string    `json:"event"`
	Actor       string    `json:"actor,omitempty"`
	Message     string    `json:"message,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

type AlertQuery struct {
	Status   string
	Severity string
	Scope    string
}
