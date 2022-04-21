/*
Copyright 2022.

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
	"fmt"
	"io/ioutil"
	"k8s.io/client-go/tools/clientcmd"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/kind/pkg/cluster/nodes"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	kindcluster "sigs.k8s.io/kind/pkg/cluster"

	infrastructurev1beta1 "github.com/grigoriymikhalkin/cluster-api-provider-kind/api/v1beta1"
)

const (
	kindClusterFinalizer = "kindcluster.infrastructure.cluster.x-k8s.io/v1beta1"
)

// KindClusterReconciler reconciles a KindCluster object
type KindClusterReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Provider *kindcluster.Provider
}

//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=kindclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=kindclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=kindclusters/finalizers,verbs=update

func (r *KindClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Starting KindCluster reconcilation")

	kindCluster := &infrastructurev1beta1.KindCluster{}
	if err := r.Client.Get(ctx, req.NamespacedName, kindCluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	defer func() {
		r.Client.Update(ctx, kindCluster)
		r.Client.Status().Update(ctx, kindCluster)
	}()

	// Check if owner Cluster resource exists
	cluster, err := util.GetOwnerCluster(ctx, r.Client, kindCluster.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get OwnerCluster: %w", err)
	}
	if cluster == nil {
		logger.Info("Waiting for cluster controller to set OwnerRef to KindCluster")
		return ctrl.Result{Requeue: true}, nil
	}

	// Check if cluster is paused
	if annotations.IsPaused(cluster, kindCluster) {
		logger.Info("Reconcilation is paused for this object")
		return ctrl.Result{Requeue: true}, nil
	}

	if !kindCluster.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.delete(logger, kindCluster)
	}

	return r.reconcile(ctx, logger, kindCluster)
}

func (r *KindClusterReconciler) delete(logger logr.Logger, kindCluster *infrastructurev1beta1.KindCluster) (ctrl.Result, error) {
	logger.Info("Deleting KindCluster")

	if err := r.Provider.Delete(kindCluster.Spec.Name, kindCluster.Status.KubeconfigPath); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to delete Kind cluster: %w", err)
	}
	os.Remove(kindCluster.Status.KubeconfigPath)

	controllerutil.RemoveFinalizer(kindCluster, kindClusterFinalizer)
	logger.Info("KindCluster was successfully deleted")

	return ctrl.Result{}, nil
}

func (r *KindClusterReconciler) reconcile(ctx context.Context, logger logr.Logger, kindCluster *infrastructurev1beta1.KindCluster) (ctrl.Result, error) {
	controllerutil.AddFinalizer(kindCluster, kindClusterFinalizer)

	var (
		err      error
		nodeList []nodes.Node
	)
	if nodeList, err = r.Provider.ListNodes(kindCluster.Spec.Name); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list Kind nodes: %w", err)
	}

	// Create Cluster if it's not already exists
	if len(nodeList) < 1 {
		tmp, err := ioutil.TempFile("/tmp", "kubeconfig-")
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create kubeconfig file: %w", err)
		}

		if err = r.Provider.Create(
			kindCluster.Spec.Name,
			kindcluster.CreateWithKubeconfigPath(tmp.Name()),
		); err != nil {
			os.Remove(tmp.Name())
			return ctrl.Result{}, fmt.Errorf("failed to create Kind cluster: %w", err)
		}

		kindCluster.Status.KubeconfigPath = tmp.Name()
		r.Client.Status().Update(ctx, kindCluster)
		r.Client.Update(ctx, kindCluster)

		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	if kindCluster.Spec.ControlPlaneEndpoint.Host == "" {
		kc, err := r.Provider.KubeConfig(kindCluster.Spec.Name, false)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to fetch kubeconfig for a cluster: %w", err)
		}

		conf, err := clientcmd.RESTConfigFromKubeConfig([]byte(kc))
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to parse kubeconfig: %w", err)
		}

		a := strings.Split(conf.Host, ":")
		hostIP := a[1][2:]
		port, err := strconv.Atoi(a[2])
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to parse port: %w", err)
		}
		kindCluster.Spec.ControlPlaneEndpoint.Host = hostIP
		kindCluster.Spec.ControlPlaneEndpoint.Port = int32(port)
	}

	kindCluster.Status.Ready = true
	r.Client.Update(ctx, kindCluster)
	r.Client.Status().Update(ctx, kindCluster)

	logger.Info("Successfully finished KindCluster reconcilation")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *KindClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrastructurev1beta1.KindCluster{}).
		Complete(r)
}
