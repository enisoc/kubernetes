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

package replication

import (
	"fmt"
	"time"

	"k8s.io/api/core/v1"
	extensionsv1beta1 "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	coreinformers "k8s.io/client-go/informers/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	v1client "k8s.io/client-go/kubernetes/typed/core/v1"
	extensionsv1beta1client "k8s.io/client-go/kubernetes/typed/extensions/v1beta1"
	v1listers "k8s.io/client-go/listers/core/v1"
	extensionslisters "k8s.io/client-go/listers/extensions/v1beta1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/api"
	apiv1 "k8s.io/kubernetes/pkg/api/v1"
	"k8s.io/kubernetes/pkg/apis/extensions"
	extensionsinternalv1beta1 "k8s.io/kubernetes/pkg/apis/extensions/v1beta1"
	"k8s.io/kubernetes/pkg/controller"
)

// informerAdapter implements ReplicaSetInformer by wrapping ReplicationControllerInformer
// and converting objects.
type informerAdapter struct {
	rcInformer coreinformers.ReplicationControllerInformer
}

func (i informerAdapter) Informer() cache.SharedIndexInformer {
	return conversionInformer{i.rcInformer.Informer()}
}

func (i informerAdapter) Lister() extensionslisters.ReplicaSetLister {
	return conversionLister{i.rcInformer.Lister()}
}

type conversionInformer struct {
	cache.SharedIndexInformer
}

func (i conversionInformer) AddEventHandler(handler cache.ResourceEventHandler) {
	i.SharedIndexInformer.AddEventHandler(conversionEventHandler{handler})
}

func (i conversionInformer) AddEventHandlerWithResyncPeriod(handler cache.ResourceEventHandler, resyncPeriod time.Duration) {
	i.SharedIndexInformer.AddEventHandlerWithResyncPeriod(conversionEventHandler{handler}, resyncPeriod)
}

type conversionLister struct {
	rcLister v1listers.ReplicationControllerLister
}

func (l conversionLister) List(selector labels.Selector) ([]*extensionsv1beta1.ReplicaSet, error) {
	rcList, err := l.rcLister.List(selector)
	if err != nil {
		return nil, err
	}
	return convertSlice(rcList)
}

func (l conversionLister) ReplicaSets(namespace string) extensionslisters.ReplicaSetNamespaceLister {
	return conversionNamespaceLister{l.rcLister.ReplicationControllers(namespace)}
}

func (l conversionLister) GetPodReplicaSets(pod *v1.Pod) ([]*extensionsv1beta1.ReplicaSet, error) {
	rcList, err := l.rcLister.GetPodControllers(pod)
	if err != nil {
		return nil, err
	}
	return convertSlice(rcList)
}

type conversionNamespaceLister struct {
	rcLister v1listers.ReplicationControllerNamespaceLister
}

func (l conversionNamespaceLister) List(selector labels.Selector) ([]*extensionsv1beta1.ReplicaSet, error) {
	rcList, err := l.rcLister.List(selector)
	if err != nil {
		return nil, err
	}
	return convertSlice(rcList)
}

func (l conversionNamespaceLister) Get(name string) (*extensionsv1beta1.ReplicaSet, error) {
	rc, err := l.rcLister.Get(name)
	if err != nil {
		return nil, err
	}
	return ConvertRCtoRS(rc, nil)
}

type conversionEventHandler struct {
	handler cache.ResourceEventHandler
}

func (h conversionEventHandler) OnAdd(obj interface{}) {
	if rs, err := ConvertRCtoRS(obj.(*v1.ReplicationController), nil); err == nil {
		obj = rs
	}
	h.handler.OnAdd(obj)
}

func (h conversionEventHandler) OnUpdate(oldObj, newObj interface{}) {
	if oldRS, err := ConvertRCtoRS(oldObj.(*v1.ReplicationController), nil); err == nil {
		oldObj = oldRS
	}
	if newRS, err := ConvertRCtoRS(newObj.(*v1.ReplicationController), nil); err == nil {
		newObj = newRS
	}
	h.handler.OnUpdate(oldObj, newObj)
}

func (h conversionEventHandler) OnDelete(obj interface{}) {
	var rs extensionsv1beta1.ReplicaSet
	if api.Scheme.Convert(obj, &rs, nil) == nil {
		obj = &rs
	}
	h.handler.OnDelete(obj)
}

type clientsetAdapter struct {
	clientset.Interface
}

func (c clientsetAdapter) ExtensionsV1beta1() extensionsv1beta1client.ExtensionsV1beta1Interface {
	return conversionExtensionsClient{c.Interface, c.Interface.ExtensionsV1beta1()}
}

func (c clientsetAdapter) Extensions() extensionsv1beta1client.ExtensionsV1beta1Interface {
	return conversionExtensionsClient{c.Interface, c.Interface.Extensions()}
}

type conversionExtensionsClient struct {
	clientset clientset.Interface
	extensionsv1beta1client.ExtensionsV1beta1Interface
}

func (c conversionExtensionsClient) ReplicaSets(namespace string) extensionsv1beta1client.ReplicaSetInterface {
	return conversionClient{c.clientset.CoreV1().ReplicationControllers(namespace)}
}

type conversionClient struct {
	v1client.ReplicationControllerInterface
}

func (c conversionClient) Create(rs *extensionsv1beta1.ReplicaSet) (*extensionsv1beta1.ReplicaSet, error) {
	return convertPut(rs, c.ReplicationControllerInterface.Create)
}

func (c conversionClient) Update(rs *extensionsv1beta1.ReplicaSet) (*extensionsv1beta1.ReplicaSet, error) {
	return convertPut(rs, c.ReplicationControllerInterface.Update)
}

func (c conversionClient) UpdateStatus(rs *extensionsv1beta1.ReplicaSet) (*extensionsv1beta1.ReplicaSet, error) {
	return convertPut(rs, c.ReplicationControllerInterface.UpdateStatus)
}

func (c conversionClient) Get(name string, options metav1.GetOptions) (*extensionsv1beta1.ReplicaSet, error) {
	rc, err := c.ReplicationControllerInterface.Get(name, options)
	if err != nil {
		return nil, err
	}
	return ConvertRCtoRS(rc, nil)
}

func (c conversionClient) List(opts metav1.ListOptions) (*extensionsv1beta1.ReplicaSetList, error) {
	rcList, err := c.ReplicationControllerInterface.List(opts)
	if err != nil {
		return nil, err
	}
	return convertList(rcList)
}

func (c conversionClient) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	panic("not implemented")
}

func (c conversionClient) Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *extensionsv1beta1.ReplicaSet, err error) {
	panic("not implemented")
}

func convertSlice(rcList []*v1.ReplicationController) ([]*extensionsv1beta1.ReplicaSet, error) {
	rsList := make([]*extensionsv1beta1.ReplicaSet, 0, len(rcList))
	for _, rc := range rcList {
		rs, err := ConvertRCtoRS(rc, nil)
		if err != nil {
			return nil, err
		}
		rsList = append(rsList, rs)
	}
	return rsList, nil
}

func convertList(rcList *v1.ReplicationControllerList) (*extensionsv1beta1.ReplicaSetList, error) {
	rsList := &extensionsv1beta1.ReplicaSetList{Items: make([]extensionsv1beta1.ReplicaSet, len(rcList.Items))}
	for i := range rcList.Items {
		rc := &rcList.Items[i]
		_, err := ConvertRCtoRS(rc, &rsList.Items[i])
		if err != nil {
			return nil, err
		}
	}
	return rsList, nil
}

func convertPut(rs *extensionsv1beta1.ReplicaSet, putRC func(*v1.ReplicationController) (*v1.ReplicationController, error)) (*extensionsv1beta1.ReplicaSet, error) {
	rc, err := ConvertRStoRC(rs)
	if err != nil {
		return nil, err
	}
	newRC, err := putRC(rc)
	if err != nil {
		return nil, err
	}
	return ConvertRCtoRS(newRC, nil)
}

func ConvertRCtoRS(rc *v1.ReplicationController, out *extensionsv1beta1.ReplicaSet) (*extensionsv1beta1.ReplicaSet, error) {
	var rsInternal extensions.ReplicaSet
	if err := apiv1.Convert_v1_ReplicationController_to_extensions_ReplicaSet(rc, &rsInternal, nil); err != nil {
		return nil, fmt.Errorf("can't convert ReplicationController %v/%v to ReplicaSet: %v", rc.Namespace, rc.Name, err)
	}
	if out == nil {
		out = new(extensionsv1beta1.ReplicaSet)
	}
	if err := extensionsinternalv1beta1.Convert_extensions_ReplicaSet_To_v1beta1_ReplicaSet(&rsInternal, out, nil); err != nil {
		return nil, fmt.Errorf("can't convert ReplicaSet (converted from ReplicationController %v/%v) from internal to extensions/v1beta1: %v", rc.Namespace, rc.Name, err)
	}
	return out, nil
}

func ConvertRStoRC(rs *extensionsv1beta1.ReplicaSet) (*v1.ReplicationController, error) {
	var rsInternal extensions.ReplicaSet
	if err := extensionsinternalv1beta1.Convert_v1beta1_ReplicaSet_To_extensions_ReplicaSet(rs, &rsInternal, nil); err != nil {
		return nil, fmt.Errorf("can't convert ReplicaSet (converting to ReplicationController %v/%v) from extensions/v1beta1 to internal: %v", rs.Namespace, rs.Name, err)
	}
	var rc v1.ReplicationController
	if err := apiv1.Convert_extensions_ReplicaSet_to_v1_ReplicationController(&rsInternal, &rc, nil); err != nil {
		return nil, fmt.Errorf("can't convert ReplicaSet to ReplicationController %v/%v: %v", rs.Namespace, rs.Name, err)
	}
	return &rc, nil
}

type podControlAdapter struct {
	controller.PodControlInterface
}

func (pc podControlAdapter) CreatePods(namespace string, template *v1.PodTemplateSpec, object runtime.Object) error {
	rc, err := ConvertRStoRC(object.(*extensionsv1beta1.ReplicaSet))
	if err != nil {
		return err
	}
	return pc.PodControlInterface.CreatePods(namespace, template, rc)
}

func (pc podControlAdapter) CreatePodsOnNode(nodeName, namespace string, template *v1.PodTemplateSpec, object runtime.Object, controllerRef *metav1.OwnerReference) error {
	rc, err := ConvertRStoRC(object.(*extensionsv1beta1.ReplicaSet))
	if err != nil {
		return err
	}
	return pc.PodControlInterface.CreatePodsOnNode(nodeName, namespace, template, rc, controllerRef)
}

func (pc podControlAdapter) CreatePodsWithControllerRef(namespace string, template *v1.PodTemplateSpec, object runtime.Object, controllerRef *metav1.OwnerReference) error {
	rc, err := ConvertRStoRC(object.(*extensionsv1beta1.ReplicaSet))
	if err != nil {
		return err
	}
	return pc.PodControlInterface.CreatePodsWithControllerRef(namespace, template, rc, controllerRef)
}

func (pc podControlAdapter) DeletePod(namespace string, podID string, object runtime.Object) error {
	rc, err := ConvertRStoRC(object.(*extensionsv1beta1.ReplicaSet))
	if err != nil {
		return err
	}
	return pc.PodControlInterface.DeletePod(namespace, podID, rc)
}
