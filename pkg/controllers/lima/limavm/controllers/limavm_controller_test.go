// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"testing"

	"gotest.tools/v3/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
)

func TestHasCondition(t *testing.T) {
	r := &LimaVMReconciler{}

	tests := []struct {
		name          string
		conditions    []metav1.Condition
		conditionType string
		status        metav1.ConditionStatus
		want          bool
	}{
		{
			name:          "empty conditions",
			conditions:    nil,
			conditionType: ConditionInstanceCreated,
			status:        metav1.ConditionTrue,
			want:          false,
		},
		{
			name: "condition exists with matching status",
			conditions: []metav1.Condition{
				{Type: ConditionInstanceCreated, Status: metav1.ConditionTrue},
			},
			conditionType: ConditionInstanceCreated,
			status:        metav1.ConditionTrue,
			want:          true,
		},
		{
			name: "condition exists with different status",
			conditions: []metav1.Condition{
				{Type: ConditionInstanceCreated, Status: metav1.ConditionFalse},
			},
			conditionType: ConditionInstanceCreated,
			status:        metav1.ConditionTrue,
			want:          false,
		},
		{
			name: "different condition type",
			conditions: []metav1.Condition{
				{Type: ConditionInstanceRunning, Status: metav1.ConditionTrue},
			},
			conditionType: ConditionInstanceCreated,
			status:        metav1.ConditionTrue,
			want:          false,
		},
		{
			name: "multiple conditions",
			conditions: []metav1.Condition{
				{Type: ConditionInstanceCreated, Status: metav1.ConditionTrue},
				{Type: ConditionInstanceRunning, Status: metav1.ConditionFalse},
			},
			conditionType: ConditionInstanceRunning,
			status:        metav1.ConditionFalse,
			want:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limaVM := &v1alpha1.LimaVM{
				Status: v1alpha1.LimaVMStatus{
					Conditions: tt.conditions,
				},
			}
			got := r.hasCondition(limaVM, tt.conditionType, tt.status)
			if got != tt.want {
				t.Errorf("hasCondition() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasConditionWithReason(t *testing.T) {
	r := &LimaVMReconciler{}

	tests := []struct {
		name          string
		conditions    []metav1.Condition
		conditionType string
		status        metav1.ConditionStatus
		reason        string
		want          bool
	}{
		{
			name:          "empty conditions",
			conditions:    nil,
			conditionType: ConditionInstanceRunning,
			status:        metav1.ConditionFalse,
			reason:        ReasonStopped,
			want:          false,
		},
		{
			name: "condition with matching status and reason",
			conditions: []metav1.Condition{
				{Type: ConditionInstanceRunning, Status: metav1.ConditionFalse, Reason: ReasonStopped},
			},
			conditionType: ConditionInstanceRunning,
			status:        metav1.ConditionFalse,
			reason:        ReasonStopped,
			want:          true,
		},
		{
			name: "condition with matching status but different reason",
			conditions: []metav1.Condition{
				{Type: ConditionInstanceRunning, Status: metav1.ConditionFalse, Reason: ReasonStartFailed},
			},
			conditionType: ConditionInstanceRunning,
			status:        metav1.ConditionFalse,
			reason:        ReasonStopped,
			want:          false,
		},
		{
			name: "condition with different status but matching reason",
			conditions: []metav1.Condition{
				{Type: ConditionInstanceRunning, Status: metav1.ConditionTrue, Reason: ReasonStopped},
			},
			conditionType: ConditionInstanceRunning,
			status:        metav1.ConditionFalse,
			reason:        ReasonStopped,
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limaVM := &v1alpha1.LimaVM{
				Status: v1alpha1.LimaVMStatus{
					Conditions: tt.conditions,
				},
			}
			got := r.hasConditionWithReason(limaVM, tt.conditionType, tt.status, tt.reason)
			if got != tt.want {
				t.Errorf("hasConditionWithReason() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConditionExists(t *testing.T) {
	r := &LimaVMReconciler{}

	tests := []struct {
		name          string
		conditions    []metav1.Condition
		conditionType string
		want          bool
	}{
		{
			name:          "empty conditions",
			conditions:    nil,
			conditionType: ConditionInstanceCreated,
			want:          false,
		},
		{
			name: "condition exists",
			conditions: []metav1.Condition{
				{Type: ConditionInstanceCreated, Status: metav1.ConditionTrue},
			},
			conditionType: ConditionInstanceCreated,
			want:          true,
		},
		{
			name: "condition does not exist",
			conditions: []metav1.Condition{
				{Type: ConditionInstanceRunning, Status: metav1.ConditionTrue},
			},
			conditionType: ConditionInstanceCreated,
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limaVM := &v1alpha1.LimaVM{
				Status: v1alpha1.LimaVMStatus{
					Conditions: tt.conditions,
				},
			}
			got := r.conditionExists(limaVM, tt.conditionType)
			if got != tt.want {
				t.Errorf("conditionExists() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetCondition(t *testing.T) {
	r := &LimaVMReconciler{}

	t.Run("add new condition", func(t *testing.T) {
		limaVM := &v1alpha1.LimaVM{}
		r.setCondition(limaVM, ConditionInstanceCreated, metav1.ConditionTrue, ReasonCreated, "test message")

		assert.Equal(t, len(limaVM.Status.Conditions), 1, "expected 1 condition")
		c := limaVM.Status.Conditions[0]
		if c.Type != ConditionInstanceCreated {
			t.Errorf("Type = %q, want %q", c.Type, ConditionInstanceCreated)
		}
		if c.Status != metav1.ConditionTrue {
			t.Errorf("Status = %v, want %v", c.Status, metav1.ConditionTrue)
		}
		if c.Reason != ReasonCreated {
			t.Errorf("Reason = %q, want %q", c.Reason, ReasonCreated)
		}
		if c.Message != "test message" {
			t.Errorf("Message = %q, want %q", c.Message, "test message")
		}
	})

	t.Run("update existing condition status", func(t *testing.T) {
		limaVM := &v1alpha1.LimaVM{
			Status: v1alpha1.LimaVMStatus{
				Conditions: []metav1.Condition{
					{
						Type:   ConditionInstanceCreated,
						Status: metav1.ConditionUnknown,
						Reason: ReasonPending,
					},
				},
			},
		}
		r.setCondition(limaVM, ConditionInstanceCreated, metav1.ConditionTrue, ReasonCreated, "created")

		assert.Equal(t, len(limaVM.Status.Conditions), 1, "expected 1 condition")
		c := limaVM.Status.Conditions[0]
		if c.Status != metav1.ConditionTrue {
			t.Errorf("Status = %v, want %v", c.Status, metav1.ConditionTrue)
		}
		if c.Reason != ReasonCreated {
			t.Errorf("Reason = %q, want %q", c.Reason, ReasonCreated)
		}
	})

	t.Run("update existing condition reason only", func(t *testing.T) {
		limaVM := &v1alpha1.LimaVM{
			Status: v1alpha1.LimaVMStatus{
				Conditions: []metav1.Condition{
					{
						Type:    ConditionInstanceRunning,
						Status:  metav1.ConditionFalse,
						Reason:  ReasonStartFailed,
						Message: "start failed",
					},
				},
			},
		}
		r.setCondition(limaVM, ConditionInstanceRunning, metav1.ConditionFalse, ReasonStopped, "stopped")

		assert.Equal(t, len(limaVM.Status.Conditions), 1, "expected 1 condition")
		c := limaVM.Status.Conditions[0]
		if c.Status != metav1.ConditionFalse {
			t.Errorf("Status = %v, want %v", c.Status, metav1.ConditionFalse)
		}
		if c.Reason != ReasonStopped {
			t.Errorf("Reason = %q, want %q", c.Reason, ReasonStopped)
		}
		if c.Message != "stopped" {
			t.Errorf("Message = %q, want %q", c.Message, "stopped")
		}
	})

	t.Run("add second condition type", func(t *testing.T) {
		limaVM := &v1alpha1.LimaVM{
			Status: v1alpha1.LimaVMStatus{
				Conditions: []metav1.Condition{
					{Type: ConditionInstanceCreated, Status: metav1.ConditionTrue},
				},
			},
		}
		r.setCondition(limaVM, ConditionInstanceRunning, metav1.ConditionFalse, ReasonStopped, "stopped")

		assert.Equal(t, len(limaVM.Status.Conditions), 2, "expected 2 conditions")
	})
}
