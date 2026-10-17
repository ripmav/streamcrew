// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
)

// BackupInfo is one backup of the profile.
type BackupInfo struct {
	// ID is the backup ID (the file name without the extension).
	ID string
	// CreatedAt is when the backup was created.
	CreatedAt time.Time
	// SchemaVersion is the profile schema version of the backup.
	SchemaVersion int64
	// Size is the backup size in bytes.
	Size int64
	// Kind is the backup kind: "scheduled", "manual", "pre-migration",
	// "pre-restore" or "pre-delete".
	Kind string
}

// Backup is the port the BackupService handler needs.
type Backup interface {
	// Create creates a manual backup and returns it.
	Create(ctx context.Context) (BackupInfo, error)
	// List lists the backups of the profile, newest first.
	List(ctx context.Context) ([]BackupInfo, error)
	// Restore restores the backup with the ID, replacing the profile data.
	Restore(ctx context.Context, id string) (int64, error)
}

// ErrRestoreRunning is returned by a Backup port when a restore is requested
// while the app is running: restoring replaces the open profile database, so
// it is only possible while the app is stopped (ADR-0012).
var ErrRestoreRunning = errors.New("restore is only possible while the app is stopped")

// backupService implements the BackupService handler.
type backupService struct {
	b Backup
}

// view converts a backup to its protocol message.
func (s *backupService) view(info BackupInfo) *v1alpha1.BackupView {
	return &v1alpha1.BackupView{
		Id:            info.ID,
		CreatedAt:     timestamppb.New(info.CreatedAt),
		SchemaVersion: uint32(info.SchemaVersion), //nolint:gosec // a schema version fits in uint32
		Size:          uint64(info.Size),          //nolint:gosec // a backup size fits in uint64
		Kind:          info.Kind,
	}
}

func (s *backupService) Create(ctx context.Context, _ *connect.Request[v1alpha1.CreateBackupRequest]) (*connect.Response[v1alpha1.CreateBackupResponse], error) {
	info, err := s.b.Create(ctx)
	if err != nil {
		return nil, apiError(err)
	}
	return connect.NewResponse(&v1alpha1.CreateBackupResponse{Backup: s.view(info)}), nil
}

func (s *backupService) List(ctx context.Context, _ *connect.Request[v1alpha1.ListBackupsRequest]) (*connect.Response[v1alpha1.ListBackupsResponse], error) {
	infos, err := s.b.List(ctx)
	if err != nil {
		return nil, apiError(err)
	}
	out := &v1alpha1.ListBackupsResponse{}
	for _, info := range infos {
		out.Backups = append(out.Backups, s.view(info))
	}
	return connect.NewResponse(out), nil
}

func (s *backupService) Restore(ctx context.Context, req *connect.Request[v1alpha1.RestoreBackupRequest]) (*connect.Response[v1alpha1.RestoreBackupResponse], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id is required"))
	}
	schema, err := s.b.Restore(ctx, req.Msg.Id)
	if err != nil {
		return nil, apiError(err)
	}
	return connect.NewResponse(&v1alpha1.RestoreBackupResponse{SchemaVersion: uint32(schema)}), nil //nolint:gosec // a schema version fits in uint32
}
