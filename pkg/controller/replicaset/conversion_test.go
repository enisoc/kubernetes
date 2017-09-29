/*
Copyright 2017 The Kubernetes Authors.

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

package replicaset_test

import (
	"testing"
	"time"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubernetes/pkg/controller"
	. "k8s.io/kubernetes/pkg/controller/replicaset"
	"k8s.io/kubernetes/pkg/controller/replication"
)

func newTestReplicationManagerFromClient(client clientset.Interface, stopCh chan struct{}, burstReplicas int) (*replication.ReplicationManager, informers.SharedInformerFactory) {
	informers := informers.NewSharedInformerFactory(client, controller.NoResyncPeriodFunc())

	ret := replication.NewReplicationManager(
		informers.Core().V1().Pods(),
		informers.Core().V1().ReplicationControllers(),
		client,
		burstReplicas,
	)
	ret.SetAlwaysReady()
	return ret, informers
}

func newPodRC(name string, rc *v1.ReplicationController, status v1.PodPhase, lastTransitionTime *metav1.Time, properlyOwned bool) *v1.Pod {
	var conditions []v1.PodCondition
	if status == v1.PodRunning {
		condition := v1.PodCondition{Type: v1.PodReady, Status: v1.ConditionTrue}
		if lastTransitionTime != nil {
			condition.LastTransitionTime = *lastTransitionTime
		}
		conditions = append(conditions, condition)
	}
	var controllerReference metav1.OwnerReference
	if properlyOwned {
		var trueVar = true
		controllerReference = metav1.OwnerReference{UID: rc.UID, APIVersion: "v1", Kind: "ReplicationController", Name: rc.Name, Controller: &trueVar}
	}
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       rc.Namespace,
			Labels:          rc.Spec.Selector,
			OwnerReferences: []metav1.OwnerReference{controllerReference},
		},
		Status: v1.PodStatus{Phase: status, Conditions: conditions},
	}
}

func BenchmarkSyncReplicaSet(b *testing.B) {
	labelMap := map[string]string{"foo": "bar"}
	rs := NewTestReplicaSet(2, labelMap)
	client := fake.NewSimpleClientset(rs)
	stopCh := make(chan struct{})
	defer close(stopCh)
	manager, informers := NewTestReplicaSetControllerFromClient(client, stopCh, BurstReplicas)

	// A controller with 2 replicas and no active pods in the store.
	// Inactive pods should be ignored. 2 creates expected.
	informers.Extensions().V1beta1().ReplicaSets().Informer().GetIndexer().Add(rs)
	failedPod := NewTestPod("failed-pod", rs, v1.PodFailed, nil, true)
	deletedPod := NewTestPod("deleted-pod", rs, v1.PodRunning, nil, true)
	deletedPod.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	informers.Core().V1().Pods().Informer().GetIndexer().Add(failedPod)
	informers.Core().V1().Pods().Informer().GetIndexer().Add(deletedPod)

	fakePodControl := controller.FakePodControl{}
	manager.SetPodControl(&fakePodControl)
	key, err := controller.KeyFunc(rs)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.SyncReplicaSet(key)
	}
}

func BenchmarkSyncReplicationController(b *testing.B) {
	labelMap := map[string]string{"foo": "bar"}
	rc, err := replication.ConvertRStoRC(NewTestReplicaSet(2, labelMap))
	if err != nil {
		b.Fatal(err)
	}
	client := fake.NewSimpleClientset(rc)
	stopCh := make(chan struct{})
	defer close(stopCh)
	manager, informers := newTestReplicationManagerFromClient(client, stopCh, BurstReplicas)

	// A controller with 2 replicas and no active pods in the store.
	// Inactive pods should be ignored. 2 creates expected.
	informers.Core().V1().ReplicationControllers().Informer().GetIndexer().Add(rc)
	failedPod := newPodRC("failed-pod", rc, v1.PodFailed, nil, true)
	deletedPod := newPodRC("deleted-pod", rc, v1.PodRunning, nil, true)
	deletedPod.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	informers.Core().V1().Pods().Informer().GetIndexer().Add(failedPod)
	informers.Core().V1().Pods().Informer().GetIndexer().Add(deletedPod)

	fakePodControl := controller.FakePodControl{}
	manager.SetPodControl(&fakePodControl)
	key, err := controller.KeyFunc(rc)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.SyncReplicaSet(key)
	}
}
