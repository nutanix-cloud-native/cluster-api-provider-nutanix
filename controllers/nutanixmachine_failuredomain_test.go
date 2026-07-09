/*
Copyright 2026 Nutanix

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

const (
	fdTestNS   = "default"
	testMetro0 = "metro0"
	testFD0    = "fd0"
	testFD1    = "fd1"
	testSite0  = "metro0-s0"
	testSite1  = "metro0-s1"
)

// newTestMetroSite builds a NutanixMetroSite with the given name, metro reference, and preferred
// failure domain for use in unit tests.
func newTestMetroSite(name, metroRef, preferredFD, namespace string) *infrav1.NutanixMetroSite {
	return &infrav1.NutanixMetroSite{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: infrav1.NutanixMetroSiteSpec{
			MetroRef:               corev1.LocalObjectReference{Name: metroRef},
			PreferredFailureDomain: corev1.LocalObjectReference{Name: preferredFD},
		},
	}
}

func TestFindMetroSiteForNativeFD(t *testing.T) {
	tests := []struct {
		name       string
		sites      []*infrav1.NutanixMetroSite
		metroName  string
		nativeFD   string
		wantSite   string
		wantErr    bool
	}{
		{
			name: "returns matching site when metro and preferred fd both match",
			sites: []*infrav1.NutanixMetroSite{
				newTestMetroSite(testSite0, testMetro0, testFD0, fdTestNS),
				newTestMetroSite(testSite1, testMetro0, testFD1, fdTestNS),
			},
			metroName: testMetro0,
			nativeFD:  testFD0,
			wantSite:  testSite0,
		},
		{
			name: "returns other site when native fd is the second failure domain",
			sites: []*infrav1.NutanixMetroSite{
				newTestMetroSite(testSite0, testMetro0, testFD0, fdTestNS),
				newTestMetroSite(testSite1, testMetro0, testFD1, fdTestNS),
			},
			metroName: testMetro0,
			nativeFD:  testFD1,
			wantSite:  testSite1,
		},
		{
			name:      "returns empty string when no sites exist",
			sites:     nil,
			metroName: testMetro0,
			nativeFD:  testFD0,
			wantSite:  "",
		},
		{
			name: "returns empty string when metro name does not match any site",
			sites: []*infrav1.NutanixMetroSite{
				newTestMetroSite(testSite0, "other-metro", testFD0, fdTestNS),
			},
			metroName: testMetro0,
			nativeFD:  testFD0,
			wantSite:  "",
		},
		{
			name: "returns empty string when preferred fd does not match native fd",
			sites: []*infrav1.NutanixMetroSite{
				newTestMetroSite(testSite0, testMetro0, testFD1, fdTestNS),
			},
			metroName: testMetro0,
			nativeFD:  testFD0,
			wantSite:  "",
		},
		{
			name: "ignores sites in a different namespace",
			sites: []*infrav1.NutanixMetroSite{
				newTestMetroSite(testSite0, testMetro0, testFD0, "other-ns"),
			},
			metroName: testMetro0,
			nativeFD:  testFD0,
			wantSite:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			scheme := newPlacementScheme(g)

			objs := make([]client.Object, 0, len(tc.sites))
			for _, s := range tc.sites {
				objs = append(objs, s)
			}
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

			got, err := findMetroSiteForNativeFD(context.Background(), fakeClient, fdTestNS, tc.metroName, tc.nativeFD)
			if tc.wantErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(got).To(Equal(tc.wantSite))
			}
		})
	}
}
