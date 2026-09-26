/*
Copyright 2026 dasmlab.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// S3EndpointSpec defines the desired state of S3Endpoint.
type S3EndpointSpec struct {
	// CredentialsSecretRef points at a Secret holding S3 connection details.
	// Preferred keys (Thanos-style, all in one secret):
	//   endpoint, access_key, secret_key, region, insecure, forcePathStyle
	// Also accepted: AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY, and a YAML
	// `config` blob (Thanos objstore shape).
	// +kubebuilder:validation:Required
	CredentialsSecretRef SecretReference `json:"credentialsSecretRef"`

	// Endpoint overrides the secret endpoint (host[:port], no scheme).
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// Region to pass to the S3 SDK (default us-east-1).
	// +optional
	// +kubebuilder:default="us-east-1"
	Region string `json:"region,omitempty"`

	// Insecure allows HTTP (useful for MinIO / lab endpoints).
	// +optional
	Insecure *bool `json:"insecure,omitempty"`

	// ForcePathStyle forces path-style addressing (required by many MinIO setups).
	// +optional
	ForcePathStyle *bool `json:"forcePathStyle,omitempty"`

	// Interval between scrapes. Defaults to 5m.
	// +optional
	// +kubebuilder:default="5m"
	Interval *metav1.Duration `json:"interval,omitempty"`

	// FolderDepth controls how deep "folder" (prefix) enumeration goes.
	// Depth 1 = top-level prefixes only; depth 3 walks three levels.
	// +optional
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10
	FolderDepth *int32 `json:"folderDepth,omitempty"`

	// Buckets limits scraping to named buckets. Empty = discover all via ListBuckets.
	// +optional
	Buckets []string `json:"buckets,omitempty"`

	// Suspend stops scraping when true.
	// +optional
	// +kubebuilder:default=false
	Suspend bool `json:"suspend,omitempty"`
}

// SecretReference names a Secret in the same namespace.
type SecretReference struct {
	// Name of the Secret.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// S3EndpointStatus defines the observed state of S3Endpoint.
type S3EndpointStatus struct {
	// Conditions of the scrape loop.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastScrapeTime is the last completed scrape (success or failure).
	// +optional
	LastScrapeTime *metav1.Time `json:"lastScrapeTime,omitempty"`

	// LastSuccessTime is the last successful scrape.
	// +optional
	LastSuccessTime *metav1.Time `json:"lastSuccessTime,omitempty"`

	// ObservedBuckets is the count of buckets seen on the last successful scrape.
	// +optional
	ObservedBuckets int32 `json:"observedBuckets,omitempty"`

	// Message is a short human-readable scrape summary.
	// +optional
	Message string `json:"message,omitempty"`

	// Ready is true after at least one successful scrape.
	// +optional
	Ready bool `json:"ready,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=s3e
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Buckets",type=integer,JSONPath=`.status.observedBuckets`
// +kubebuilder:printcolumn:name="LastSuccess",type=date,JSONPath=`.status.lastSuccessTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// S3Endpoint is the Schema for the s3endpoints API.
type S3Endpoint struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   S3EndpointSpec   `json:"spec,omitempty"`
	Status S3EndpointStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// S3EndpointList contains a list of S3Endpoint.
type S3EndpointList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []S3Endpoint `json:"items"`
}

func init() {
	SchemeBuilder.Register(&S3Endpoint{}, &S3EndpointList{})
}
