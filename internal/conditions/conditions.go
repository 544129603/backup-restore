// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package conditions

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func Set(list *[]metav1.Condition, generation int64, conditionType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(list, metav1.Condition{
		Type: conditionType, Status: status, ObservedGeneration: generation,
		Reason: reason, Message: message,
	})
}

func True(list *[]metav1.Condition, generation int64, conditionType, reason, message string) {
	Set(list, generation, conditionType, metav1.ConditionTrue, reason, message)
}

func False(list *[]metav1.Condition, generation int64, conditionType, reason, message string) {
	Set(list, generation, conditionType, metav1.ConditionFalse, reason, message)
}

func Unknown(list *[]metav1.Condition, generation int64, conditionType, reason, message string) {
	Set(list, generation, conditionType, metav1.ConditionUnknown, reason, message)
}

func IsTrue(list []metav1.Condition, conditionType string) bool {
	condition := meta.FindStatusCondition(list, conditionType)
	return condition != nil && condition.Status == metav1.ConditionTrue
}

func ObjectRef(groupVersion schema.GroupVersion, resource, namespace, name string) string {
	if namespace == "" {
		return groupVersion.String() + "/" + resource + "/" + name
	}
	return groupVersion.String() + "/" + resource + "/" + namespace + "/" + name
}
