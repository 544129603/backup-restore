// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func metav1LabelSelector(selector *metav1.LabelSelector) (labels.Selector, error) {
	return metav1.LabelSelectorAsSelector(selector)
}
