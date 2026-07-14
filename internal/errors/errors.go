// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package errors

import (
	stderrors "errors"
	"fmt"
)

const (
	CodeRepoConnect         = "BR-REPO-CONNECT-001"
	CodeRepoAuth            = "BR-REPO-AUTH-002"
	CodeRepoPath            = "BR-REPO-PATH-003"
	CodeRepoNoSpace         = "BR-REPO-NOSPACE-004"
	CodeScopeInvalid        = "BR-SCOPE-INVALID-001"
	CodeScopeDiscovery      = "BR-SCOPE-DISCOVERY-002"
	CodePolicyCron          = "BR-POLICY-CRON-001"
	CodePolicyDuplicate     = "BR-POLICY-DUPLICATE-002"
	CodeSnapshotUnsupported = "BR-SNAPSHOT-NOTSUPPORTED-001"
	CodeSnapshotCreate      = "BR-SNAPSHOT-CREATE-002"
	CodeSnapshotTimeout     = "BR-SNAPSHOT-TIMEOUT-003"
	CodeBackupCollect       = "BR-BACKUP-COLLECT-001"
	CodeBackupPackage       = "BR-BACKUP-PACKAGE-002"
	CodeBackupUpload        = "BR-BACKUP-UPLOAD-003"
	CodeBackupChecksum      = "BR-BACKUP-CHECKSUM-004"
	CodeBackupCancelled     = "BR-BACKUP-CANCELLED-005"
	CodeRecordBroken        = "BR-RECORD-BROKEN-001"
	CodeRecordNotFound      = "BR-RECORD-NOTFOUND-002"
	CodeRestorePrecheck     = "BR-RESTORE-PRECHECK-001"
	CodeRestoreConflict     = "BR-RESTORE-CONFLICT-002"
	CodeRestorePVC          = "BR-RESTORE-PVC-003"
	CodeRestoreResource     = "BR-RESTORE-RESOURCE-004"
	CodePermissionDenied    = "BR-PERMISSION-DENIED-001"
	CodeInternal            = "BR-INTERNAL-001"
)

type OperatorError struct {
	Code      string
	Message   string
	Retryable bool
	Cause     error
}

func (e *OperatorError) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
}

func (e *OperatorError) Unwrap() error { return e.Cause }

func New(code, message string, retryable bool, cause error) *OperatorError {
	return &OperatorError{Code: code, Message: message, Retryable: retryable, Cause: cause}
}

func As(err error) (*OperatorError, bool) {
	var target *OperatorError
	if stderrors.As(err, &target) {
		return target, true
	}
	return nil, false
}

func Code(err error) string {
	if typed, ok := As(err); ok {
		return typed.Code
	}
	return CodeInternal
}

func Retryable(err error) bool {
	if typed, ok := As(err); ok {
		return typed.Retryable
	}
	return false
}
