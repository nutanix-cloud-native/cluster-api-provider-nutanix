// Code generated by MockGen. DO NOT EDIT.
// Source: k8s.io/client-go/informers/core/v1 (interfaces: ConfigMapInformer,SecretInformer)
//
// Generated by this command:
//
//	mockgen -destination=mocks/k8sclient/informer.go -package=mockk8sclient k8s.io/client-go/informers/core/v1 ConfigMapInformer,SecretInformer
//

// Package mockk8sclient is a generated GoMock package.
package mockk8sclient

import (
	reflect "reflect"

	gomock "go.uber.org/mock/gomock"
	v1 "k8s.io/client-go/listers/core/v1"
	cache "k8s.io/client-go/tools/cache"
)

// MockConfigMapInformer is a mock of ConfigMapInformer interface.
type MockConfigMapInformer struct {
	ctrl     *gomock.Controller
	recorder *MockConfigMapInformerMockRecorder
}

// MockConfigMapInformerMockRecorder is the mock recorder for MockConfigMapInformer.
type MockConfigMapInformerMockRecorder struct {
	mock *MockConfigMapInformer
}

// NewMockConfigMapInformer creates a new mock instance.
func NewMockConfigMapInformer(ctrl *gomock.Controller) *MockConfigMapInformer {
	mock := &MockConfigMapInformer{ctrl: ctrl}
	mock.recorder = &MockConfigMapInformerMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockConfigMapInformer) EXPECT() *MockConfigMapInformerMockRecorder {
	return m.recorder
}

// Informer mocks base method.
func (m *MockConfigMapInformer) Informer() cache.SharedIndexInformer {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Informer")
	ret0, _ := ret[0].(cache.SharedIndexInformer)
	return ret0
}

// Informer indicates an expected call of Informer.
func (mr *MockConfigMapInformerMockRecorder) Informer() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Informer", reflect.TypeOf((*MockConfigMapInformer)(nil).Informer))
}

// Lister mocks base method.
func (m *MockConfigMapInformer) Lister() v1.ConfigMapLister {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Lister")
	ret0, _ := ret[0].(v1.ConfigMapLister)
	return ret0
}

// Lister indicates an expected call of Lister.
func (mr *MockConfigMapInformerMockRecorder) Lister() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Lister", reflect.TypeOf((*MockConfigMapInformer)(nil).Lister))
}

// MockSecretInformer is a mock of SecretInformer interface.
type MockSecretInformer struct {
	ctrl     *gomock.Controller
	recorder *MockSecretInformerMockRecorder
}

// MockSecretInformerMockRecorder is the mock recorder for MockSecretInformer.
type MockSecretInformerMockRecorder struct {
	mock *MockSecretInformer
}

// NewMockSecretInformer creates a new mock instance.
func NewMockSecretInformer(ctrl *gomock.Controller) *MockSecretInformer {
	mock := &MockSecretInformer{ctrl: ctrl}
	mock.recorder = &MockSecretInformerMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockSecretInformer) EXPECT() *MockSecretInformerMockRecorder {
	return m.recorder
}

// Informer mocks base method.
func (m *MockSecretInformer) Informer() cache.SharedIndexInformer {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Informer")
	ret0, _ := ret[0].(cache.SharedIndexInformer)
	return ret0
}

// Informer indicates an expected call of Informer.
func (mr *MockSecretInformerMockRecorder) Informer() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Informer", reflect.TypeOf((*MockSecretInformer)(nil).Informer))
}

// Lister mocks base method.
func (m *MockSecretInformer) Lister() v1.SecretLister {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Lister")
	ret0, _ := ret[0].(v1.SecretLister)
	return ret0
}

// Lister indicates an expected call of Lister.
func (mr *MockSecretInformerMockRecorder) Lister() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Lister", reflect.TypeOf((*MockSecretInformer)(nil).Lister))
}
