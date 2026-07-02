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
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Target     string    `json:"target"`
	Status     string    `json:"status"`
	CreatedBy  string    `json:"createdBy"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
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
