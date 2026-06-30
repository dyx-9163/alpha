package servers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

type DiskDevice struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	SizeHuman   string `json:"sizeHuman"`
	Model       string `json:"model,omitempty"`
	Mountpoint  string `json:"mountpoint,omitempty"`
	FSType      string `json:"fstype,omitempty"`
	ReadOnly    bool   `json:"readOnly"`
	Removable   bool   `json:"removable"`
	HasChildren bool   `json:"hasChildren"`
	Candidate   bool   `json:"candidate"`
	Reason      string `json:"reason,omitempty"`
}

type DiskInventory struct {
	ServerID string       `json:"serverId"`
	Devices  []DiskDevice `json:"devices"`
}

func (s Service) ListDiskDevices(ctx context.Context, id string) (DiskInventory, error) {
	server, err := s.store.GetServer(id, true)
	if err != nil {
		return DiskInventory{}, err
	}
	result, err := s.remote.Run(ctx, server, diskInventoryCommand())
	if err != nil {
		return DiskInventory{}, err
	}
	devices, err := ParseDiskInventory(result.Stdout)
	if err != nil {
		return DiskInventory{}, err
	}
	return DiskInventory{ServerID: id, Devices: candidateDiskDevices(devices)}, nil
}

func diskInventoryCommand() string {
	return "LC_ALL=C lsblk -b -J -e 7,11 -o NAME,PATH,TYPE,SIZE,MOUNTPOINT,FSTYPE,MODEL,RO,RM"
}

type lsblkInventory struct {
	BlockDevices []lsblkDevice `json:"blockdevices"`
}

type lsblkDevice struct {
	Name       string        `json:"name"`
	Path       string        `json:"path"`
	Type       string        `json:"type"`
	Size       any           `json:"size"`
	Model      string        `json:"model"`
	Mountpoint any           `json:"mountpoint"`
	FSType     string        `json:"fstype"`
	RO         any           `json:"ro"`
	RM         any           `json:"rm"`
	Children   []lsblkDevice `json:"children"`
}

func ParseDiskInventory(output string) ([]DiskDevice, error) {
	var raw lsblkInventory
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse lsblk output: %w", err)
	}
	out := make([]DiskDevice, 0, len(raw.BlockDevices))
	for _, device := range raw.BlockDevices {
		out = appendDiskDevice(out, device)
	}
	return out, nil
}

func appendDiskDevice(out []DiskDevice, raw lsblkDevice) []DiskDevice {
	path := strings.TrimSpace(raw.Path)
	if path == "" && strings.TrimSpace(raw.Name) != "" {
		path = "/dev/" + strings.TrimSpace(raw.Name)
	}
	device := DiskDevice{
		Name:        strings.TrimSpace(raw.Name),
		Path:        path,
		Type:        strings.TrimSpace(raw.Type),
		Size:        numberValue(raw.Size),
		Model:       strings.TrimSpace(raw.Model),
		Mountpoint:  stringValue(raw.Mountpoint),
		FSType:      strings.TrimSpace(raw.FSType),
		ReadOnly:    boolValue(raw.RO),
		Removable:   boolValue(raw.RM),
		HasChildren: len(raw.Children) > 0,
	}
	device.SizeHuman = humanBytes(device.Size)
	device.Candidate, device.Reason = diskCandidate(device)
	out = append(out, device)
	for _, child := range raw.Children {
		out = appendDiskDevice(out, child)
	}
	return out
}

func candidateDiskDevices(devices []DiskDevice) []DiskDevice {
	out := make([]DiskDevice, 0, len(devices))
	for _, device := range devices {
		if device.Candidate {
			out = append(out, device)
		}
	}
	return out
}

func diskCandidate(device DiskDevice) (bool, string) {
	if device.Path == "" || !strings.HasPrefix(device.Path, "/dev/") {
		return false, "missing-path"
	}
	if device.ReadOnly {
		return false, "read-only"
	}
	if device.Removable {
		return false, "removable"
	}
	if isOpticalFileSystem(device.FSType) {
		return false, "optical-media"
	}
	if device.Type != "disk" {
		return false, "unsupported-type"
	}
	if strings.TrimSpace(device.Mountpoint) != "" {
		return false, "mounted"
	}
	if device.Type == "disk" && device.HasChildren {
		return false, "has-partitions"
	}
	return true, ""
}

func isOpticalFileSystem(fstype string) bool {
	switch strings.ToLower(strings.TrimSpace(fstype)) {
	case "iso9660", "udf":
		return true
	default:
		return false
	}
}

func stringValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return strings.TrimSpace(v.String())
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func numberValue(value any) int64 {
	switch v := value.(type) {
	case nil:
		return 0
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	case string:
		var n int64
		_, _ = fmt.Sscanf(strings.TrimSpace(v), "%d", &n)
		return n
	default:
		return 0
	}
}

func boolValue(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	case json.Number:
		n, _ := v.Int64()
		return n != 0
	case string:
		text := strings.ToLower(strings.TrimSpace(v))
		return text == "1" || text == "true" || text == "yes"
	default:
		return false
	}
}

func humanBytes(size int64) string {
	if size <= 0 {
		return "0 B"
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	value := float64(size)
	index := 0
	for value >= 1024 && index < len(units)-1 {
		value /= 1024
		index++
	}
	if index == 0 {
		return fmt.Sprintf("%d %s", size, units[index])
	}
	rounded := math.Round(value*10) / 10
	if rounded == math.Trunc(rounded) {
		return fmt.Sprintf("%.0f %s", rounded, units[index])
	}
	return fmt.Sprintf("%.1f %s", rounded, units[index])
}
