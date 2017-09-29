/*
Copyright 2015 The Kubernetes Authors.

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

package perf

import (
	"fmt"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/controller/replication"
	"k8s.io/kubernetes/test/integration/framework"
)

func testLabels() map[string]string {
	return map[string]string{"name": "test"}
}

func newRC(name, namespace string, replicas int) *v1.ReplicationController {
	replicasCopy := int32(replicas)
	return &v1.ReplicationController{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ReplicationController",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: v1.ReplicationControllerSpec{
			Selector: testLabels(),
			Replicas: &replicasCopy,
			Template: &v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: testLabels(),
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "fake-name",
							Image: "fakeimage",
						},
					},
				},
			},
		},
	}
}

func rmSetup(t *testing.T) (*httptest.Server, framework.CloseFunc, *replication.ReplicationManager, informers.SharedInformerFactory, clientset.Interface) {
	masterConfig := framework.NewIntegrationTestMasterConfig()
	_, s, closeFn := framework.RunAMaster(masterConfig)

	config := restclient.Config{Host: s.URL}
	clientSet, err := clientset.NewForConfig(&config)
	if err != nil {
		t.Fatalf("Error in create clientset: %v", err)
	}
	resyncPeriod := 12 * time.Hour

	informers := informers.NewSharedInformerFactory(clientSet, resyncPeriod)
	rm := replication.NewReplicationManager(informers.Core().V1().Pods(), informers.Core().V1().ReplicationControllers(), clientSet, replication.BurstReplicas)

	return s, closeFn, rm, informers, clientSet
}

const (
	benchControllers = 100
	benchReplicas    = 1
)

func TestRCThroughput(t *testing.T) {
	stopCh := make(chan struct{})
	_, closeFn, rm, informers, clientSet := rmSetup(t)
	defer closeFn()
	defer close(stopCh)
	ns := "rc-bench"

	rcClient := clientSet.Core().ReplicationControllers(ns)

	var createdPods int64
	expectedPods := int64(benchControllers * benchReplicas)
	done := make(chan struct{})
	informers.Core().V1().Pods().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			count := atomic.AddInt64(&createdPods, int64(1))
			fmt.Printf("%v pods\n", count)
			if count == expectedPods {
				close(done)
			}
		},
	})
	informers.Start(stopCh)

	for i := 0; i < benchControllers; i++ {
		name := fmt.Sprintf("rc-%v", i)
		_, err := rcClient.Create(newRC(name, ns, benchReplicas))
		if err != nil {
			t.Fatalf("Failed to create replication controller %v: %v", name, err)
		}
	}

	wait.Poll(time.Second, time.Minute, func() (bool, error) {
		list, err := informers.Core().V1().ReplicationControllers().Lister().List(labels.Everything())
		if err != nil {
			return false, err
		}
		return len(list) == benchControllers, nil
	})

	start := time.Now()
	go rm.Run(5, stopCh)
	<-done
	elapsed := time.Since(start)

	fmt.Printf("RC throughput: %v/s\n", benchControllers/elapsed.Seconds())
}
