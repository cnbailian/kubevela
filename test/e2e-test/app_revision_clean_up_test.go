/*
Copyright 2021 The KubeVela Authors.

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

package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/ghodss/yaml"
	"github.com/oam-dev/kubevela/apis/core.oam.dev/v1alpha2"
	"github.com/oam-dev/kubevela/apis/core.oam.dev/v1beta1"
	"github.com/oam-dev/kubevela/apis/standard.oam.dev/v1alpha1"
	"github.com/oam-dev/kubevela/pkg/oam"
	"github.com/oam-dev/kubevela/pkg/oam/util"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	appRevisionLimit = 5
)

var _ = Describe("Test application controller clean up appRevision", func() {
	ctx := context.TODO()
	namespace := "clean-up-revision"

	cd := &v1beta1.ComponentDefinition{}
	cdDefJson, _ := yaml.YAMLToJSON([]byte(compDefYaml))

	BeforeEach(func() {
		ns := v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}
		Expect(k8sClient.Create(ctx, &ns)).Should(SatisfyAny(BeNil(), &util.AlreadyExistMatcher{}))

		Expect(json.Unmarshal(cdDefJson, cd)).Should(BeNil())
		Expect(k8sClient.Create(ctx, cd.DeepCopy())).Should(SatisfyAny(BeNil(), &util.AlreadyExistMatcher{}))
	})

	AfterEach(func() {
		By("[TEST] Clean up resources after an integration test")
		k8sClient.DeleteAllOf(ctx, &v1beta1.Application{}, client.InNamespace(namespace))
		k8sClient.DeleteAllOf(ctx, &v1beta1.ComponentDefinition{}, client.InNamespace(namespace))
		k8sClient.DeleteAllOf(ctx, &v1beta1.WorkloadDefinition{}, client.InNamespace(namespace))
		k8sClient.DeleteAllOf(ctx, &v1beta1.TraitDefinition{}, client.InNamespace(namespace))

		Expect(k8sClient.Delete(ctx, &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, client.PropagationPolicy(metav1.DeletePropagationForeground))).Should(Succeed())
		// guarantee namespace have been deleted
		Eventually(func() error {
			ns := new(v1.Namespace)
			err := k8sClient.Get(ctx, types.NamespacedName{Name: namespace}, ns)
			if err == nil {
				return fmt.Errorf("namespace still exist")
			}
			return nil
		}, time.Second*60, time.Microsecond*300).Should(BeNil())
	})

	It("Test clean up appRevision", func() {
		appName := "app-1"
		appKey := types.NamespacedName{Namespace: namespace, Name: appName}
		app := getApp(appName, namespace, "normal-worker")
		Expect(k8sClient.Create(ctx, app)).Should(BeNil())
		checkApp := new(v1beta1.Application)
		for i := 0; i < appRevisionLimit+1; i++ {
			Eventually(func() error {
				checkApp = new(v1beta1.Application)
				Expect(k8sClient.Get(ctx, appKey, checkApp)).Should(BeNil())
				if checkApp.Status.LatestRevision == nil || checkApp.Status.LatestRevision.Revision != int64(i+1) {
					return fmt.Errorf("application point to wrong revision")
				}
				return nil
			}, time.Second*30, time.Microsecond*300).Should(BeNil())
			Eventually(func() error {
				checkApp = new(v1beta1.Application)
				Expect(k8sClient.Get(ctx, appKey, checkApp)).Should(BeNil())
				property := fmt.Sprintf(`{"cmd":["sleep","1000"],"image":"busybox:%d"}`, i)
				checkApp.Spec.Components[0].Properties = runtime.RawExtension{Raw: []byte(property)}
				if err := k8sClient.Update(ctx, checkApp); err != nil {
					return err
				}
				return nil
			}, time.Second*30, time.Microsecond*300).Should(BeNil())
		}
		appContext := new(v1alpha2.ApplicationContext)
		Expect(k8sClient.Get(ctx, appKey, appContext)).Should(BeNil())
		listOpts := []client.ListOption{
			client.InNamespace(namespace),
			client.MatchingLabels{
				oam.LabelAppName: appName,
			},
		}
		appRevisionList := new(v1beta1.ApplicationRevisionList)
		Eventually(func() error {
			err := k8sClient.List(ctx, appRevisionList, listOpts...)
			if err != nil {
				return err
			}
			if len(appRevisionList.Items) != appRevisionLimit+1 {
				return fmt.Errorf("error appRevison number wants %d, actually %d", appRevisionLimit+1, len(appRevisionList.Items))
			}
			return nil
		}, time.Second*30, time.Microsecond*300).Should(BeNil())
		Eventually(func() error {
			if err := k8sClient.Get(ctx, appKey, appContext); err != nil {
				return err
			}
			return nil
		}, time.Second*30, time.Microsecond*300).Should(BeNil())
		By("create new appRevision will remove appRevison1")
		Expect(k8sClient.Get(ctx, appKey, checkApp)).Should(BeNil())
		property := fmt.Sprintf(`{"cmd":["sleep","1000"],"image":"busybox:%d"}`, 6)
		checkApp.Spec.Components[0].Properties = runtime.RawExtension{Raw: []byte(property)}
		Expect(k8sClient.Update(ctx, checkApp)).Should(BeNil())
		deletedRevison := new(v1beta1.ApplicationRevision)
		revKey := types.NamespacedName{Namespace: namespace, Name: appName + "-v1"}
		Eventually(func() error {
			err := k8sClient.List(ctx, appRevisionList, listOpts...)
			if err != nil {
				return err
			}
			if len(appRevisionList.Items) != appRevisionLimit+1 {
				return fmt.Errorf("error appRevison number wants %d, actually %d", appRevisionLimit+1, len(appRevisionList.Items))
			}
			err = k8sClient.Get(ctx, revKey, deletedRevison)
			if err == nil || !apierrors.IsNotFound(err) {
				return fmt.Errorf("haven't clean up the oldest revision")
			}
			if res, err := util.CheckAppRevision(appRevisionList.Items, []int{2, 3, 4, 5, 6, 7}); err != nil || !res {
				return fmt.Errorf("appRevision collection mismatch")
			}
			return nil
		}, time.Second*30, time.Microsecond*300).Should(BeNil())

		By("update app again will gc appRevision2")
		Eventually(func() error {
			if err := k8sClient.Get(ctx, appKey, checkApp); err != nil {
				return err
			}
			property = fmt.Sprintf(`{"cmd":["sleep","1000"],"image":"busybox:%d"}`, 7)
			checkApp.Spec.Components[0].Properties = runtime.RawExtension{Raw: []byte(property)}
			if err := k8sClient.Update(ctx, checkApp); err != nil {
				return err
			}
			return nil
		})
		Eventually(func() error {
			err := k8sClient.List(ctx, appRevisionList, listOpts...)
			if err != nil {
				return err
			}
			if len(appRevisionList.Items) != appRevisionLimit+1 {
				return fmt.Errorf("error appRevison number wants %d, actually %d", appRevisionLimit+1, len(appRevisionList.Items))
			}
			revKey = types.NamespacedName{Namespace: namespace, Name: appName + "-v2"}
			err = k8sClient.Get(ctx, revKey, deletedRevison)
			if err == nil || !apierrors.IsNotFound(err) {
				return fmt.Errorf("haven't clean up the  revision-2")
			}
			if res, err := util.CheckAppRevision(appRevisionList.Items, []int{3, 4, 5, 6, 7, 8}); err != nil || !res {
				return fmt.Errorf("appRevision collection mismatch")
			}
			return nil
		}, time.Second*30, time.Microsecond*300).Should(BeNil())
	})

	It("Test clean up rollout appRevision", func() {
		appName := "app-2"
		appKey := types.NamespacedName{Namespace: namespace, Name: appName}
		app := getApp(appName, namespace, "normal-worker")
		metav1.SetMetaDataAnnotation(&app.ObjectMeta, oam.AnnotationAppRollout, "true")
		metav1.SetMetaDataAnnotation(&app.ObjectMeta, oam.AnnotationRollingComponent, "comp1")
		Expect(k8sClient.Create(ctx, app)).Should(BeNil())
		checkApp := new(v1beta1.Application)
		for i := 0; i < appRevisionLimit; i++ {
			Eventually(func() error {
				Expect(k8sClient.Get(ctx, appKey, checkApp)).Should(BeNil())
				if checkApp.Status.LatestRevision == nil || checkApp.Status.LatestRevision.Revision != int64(i+1) {
					return fmt.Errorf("application point to wrong revision")
				}
				return nil
			}, time.Second*30, time.Microsecond).Should(BeNil())
			Eventually(func() error {
				checkApp = new(v1beta1.Application)
				Expect(k8sClient.Get(ctx, appKey, checkApp)).Should(BeNil())
				property := fmt.Sprintf(`{"cmd":["sleep","1000"],"image":"busybox:%d"}`, i)
				checkApp.Spec.Components[0].Properties = runtime.RawExtension{Raw: []byte(property)}
				if err := k8sClient.Update(ctx, checkApp); err != nil {
					return err
				}
				return nil
			}, time.Second*30, time.Microsecond*300).Should(BeNil())
		}
		appContext := new(v1alpha2.ApplicationContext)
		Expect(k8sClient.Get(ctx, appKey, appContext)).Should(util.NotFoundMatcher{})
		listOpts := []client.ListOption{
			client.InNamespace(namespace),
			client.MatchingLabels{
				oam.LabelAppName: appName,
			},
		}
		appRevisionList := new(v1beta1.ApplicationRevisionList)
		Eventually(func() error {
			err := k8sClient.List(ctx, appRevisionList, listOpts...)
			if err != nil {
				return err
			}
			if len(appRevisionList.Items) != appRevisionLimit+1 {
				return fmt.Errorf("error appRevison number wants %d, actually %d", appRevisionLimit+1, len(appRevisionList.Items))
			}
			return nil
		}, time.Second*300, time.Microsecond*300).Should(BeNil())

		By("create new appRevision will remove appRevison1")
		Expect(k8sClient.Get(ctx, appKey, checkApp)).Should(BeNil())
		property := fmt.Sprintf(`{"cmd":["sleep","1000"],"image":"busybox:%d"}`, 5)
		checkApp.Spec.Components[0].Properties = runtime.RawExtension{Raw: []byte(property)}
		Expect(k8sClient.Update(ctx, checkApp)).Should(BeNil())
		deletedRevison := new(v1beta1.ApplicationRevision)
		revKey := types.NamespacedName{Namespace: namespace, Name: appName + "-v1"}
		Eventually(func() error {
			err := k8sClient.List(ctx, appRevisionList, listOpts...)
			if err != nil {
				return err
			}
			if len(appRevisionList.Items) != appRevisionLimit+1 {
				return fmt.Errorf("error appRevison number wants %d, actually %d", appRevisionLimit, len(appRevisionList.Items))
			}
			err = k8sClient.Get(ctx, revKey, deletedRevison)
			if err == nil || !apierrors.IsNotFound(err) {
				return fmt.Errorf("haven't clean up the oldest revision")
			}
			if res, err := util.CheckAppRevision(appRevisionList.Items, []int{2, 3, 4, 5, 6, 7}); err != nil || !res {
				return fmt.Errorf("appRevision collection mismatch")
			}
			return nil
		}, time.Second*30, time.Microsecond*300).Should(BeNil())

		By("update app again will gc appRevision2")
		Expect(k8sClient.Get(ctx, appKey, checkApp)).Should(BeNil())
		property = fmt.Sprintf(`{"cmd":["sleep","1000"],"image":"busybox:%d"}`, 6)
		checkApp.Spec.Components[0].Properties = runtime.RawExtension{Raw: []byte(property)}
		Expect(k8sClient.Update(ctx, checkApp)).Should(BeNil())
		Eventually(func() error {
			err := k8sClient.List(ctx, appRevisionList, listOpts...)
			if err != nil {
				return err
			}
			if len(appRevisionList.Items) != appRevisionLimit+1 {
				return fmt.Errorf("error appRevison number wants %d, actually %d", appRevisionLimit, len(appRevisionList.Items))
			}
			revKey = types.NamespacedName{Namespace: namespace, Name: appName + "-v2"}
			err = k8sClient.Get(ctx, revKey, deletedRevison)
			if err == nil || !apierrors.IsNotFound(err) {
				return fmt.Errorf("haven't clean up the  revision-2")
			}
			if res, err := util.CheckAppRevision(appRevisionList.Items, []int{3, 4, 5, 6, 7, 8}); err != nil || !res {
				return fmt.Errorf("appRevision collection mismatch")
			}
			return nil
		}, time.Second*30, time.Microsecond*300).Should(BeNil())
		appRollout := &v1beta1.AppRollout{
			TypeMeta: metav1.TypeMeta{
				APIVersion: v1beta1.AppRolloutKindAPIVersion,
				Kind:       v1beta1.AppRolloutKind,
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      "app-roll-out",
			},
			Spec: v1beta1.AppRolloutSpec{
				TargetAppRevisionName: appName + "-v3",
				ComponentList:         []string{"comp1"},
				RolloutPlan: v1alpha1.RolloutPlan{
					RolloutBatches: []v1alpha1.RolloutBatch{
						{
							Replicas: intstr.FromInt(1),
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, appRollout)).Should(BeNil())

		By("update app twice will gc appRevision4 not appRevision3")
		for i := 7; i < 9; i++ {
			Eventually(func() error {
				if err := k8sClient.Get(ctx, appKey, checkApp); err != nil {
					return err
				}
				if checkApp.Status.LatestRevision == nil || checkApp.Status.LatestRevision.Revision != int64(i+1) {
					return fmt.Errorf("application point to wrong revision")
				}
				return nil
			}, time.Second*30, time.Microsecond*300).Should(BeNil())
			Eventually(func() error {
				if err := k8sClient.Get(ctx, appKey, checkApp); err != nil {
					return err
				}
				property = fmt.Sprintf(`{"cmd":["sleep","1000"],"image":"busybox:%d"}`, i)
				checkApp.Spec.Components[0].Properties = runtime.RawExtension{Raw: []byte(property)}
				if err := k8sClient.Update(ctx, checkApp); err != nil {
					return err
				}
				return nil
			}, time.Second*30, time.Microsecond).Should(BeNil())
		}
		Eventually(func() error {
			err := k8sClient.List(ctx, appRevisionList, listOpts...)
			if err != nil {
				return err
			}
			if len(appRevisionList.Items) != appRevisionLimit+2 {
				return fmt.Errorf("error appRevison number wants %d, actually %d", appRevisionLimit+2, len(appRevisionList.Items))
			}
			revKey = types.NamespacedName{Namespace: namespace, Name: appName + "-v4"}
			err = k8sClient.Get(ctx, revKey, deletedRevison)
			if err == nil || !apierrors.IsNotFound(err) {
				return fmt.Errorf("haven't clean up the  revision-4")
			}
			existRev := new(v1beta1.ApplicationRevision)
			revKey = types.NamespacedName{Namespace: namespace, Name: appName + "-v3"}
			err = k8sClient.Get(ctx, revKey, existRev)
			if err != nil {
				return err
			}
			if res, err := util.CheckAppRevision(appRevisionList.Items, []int{3, 5, 6, 7, 8, 9, 10}); err != nil || !res {
				return fmt.Errorf("appRevision collection mismatch")
			}
			return nil
		}, time.Second*30, time.Microsecond*300).Should(BeNil())
	})
})

var (
	compDefYaml = `
apiVersion: core.oam.dev/v1beta1
kind: ComponentDefinition
metadata:
  name: normal-worker
  namespace: clean-up-revision
  annotations:
    definition.oam.dev/description: "Long-running scalable backend worker without network endpoint"
spec:
  workload:
    definition:
      apiVersion: apps/v1
      kind: Deployment
  extension:
    healthPolicy: |
      isHealth: context.output.status.readyReplicas == context.output.status.replicas
    template: |
      output: {
          apiVersion: "apps/v1"
          kind:       "Deployment"
          spec: {
              replicas: 0
              template: {
                  metadata: labels: {
                      "app.oam.dev/component": context.name
                  }

                  spec: {
                      containers: [{
                          name:  context.name
                          image: parameter.image

                          if parameter["cmd"] != _|_ {
                              command: parameter.cmd
                          }
                      }]
                  }
              }

              selector:
                  matchLabels:
                      "app.oam.dev/component": context.name
          }
      }

      parameter: {
          // +usage=Which image would you like to use for your service
          // +short=i
          image: string

          cmd?: [...string]
      }
`
)

func getApp(appName, namespace, comptype string) *v1beta1.Application {
	return &v1beta1.Application{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Application",
			APIVersion: "core.oam.dev/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      appName,
			Namespace: namespace,
		},
		Spec: v1beta1.ApplicationSpec{
			Components: []v1beta1.ApplicationComponent{
				{
					Name:       "comp1",
					Type:       comptype,
					Properties: runtime.RawExtension{Raw: []byte(`{"cmd":["sleep","1000"],"image":"busybox"}`)},
				},
			},
		},
	}
}
