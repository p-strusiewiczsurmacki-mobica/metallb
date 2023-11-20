/*


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
	"reflect"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	metallbv1beta1 "go.universe.tf/metallb/api/v1beta1"
	"go.universe.tf/metallb/internal/config"
	"go.universe.tf/metallb/internal/conversion"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type PoolReconciler struct {
	client.Client
	Logger              log.Logger
	Scheme              *runtime.Scheme
	Namespace           string
	Handler             func(log.Logger, *config.Pools) SyncState
	ValidateConfig      config.Validate
	ForceReload         func()
	currentConfig       *config.Config
	LegacyConfigMapName string
}

func (r *PoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	level.Info(r.Logger).Log("controller", "PoolReconciler", "start reconcile", req.NamespacedName.String())
	defer level.Info(r.Logger).Log("controller", "PoolReconciler", "end reconcile", req.NamespacedName.String())
	updates.Inc()

	var addressPools metallbv1beta1.AddressPoolList
	if err := r.List(ctx, &addressPools, client.InNamespace(r.Namespace)); err != nil {
		level.Error(r.Logger).Log("controller", "PoolReconciler", "message", "failed to get addresspools", "error", err)
		return ctrl.Result{}, err
	}

	var ipAddressPools metallbv1beta1.IPAddressPoolList
	if err := r.List(ctx, &ipAddressPools, client.InNamespace(r.Namespace)); err != nil {
		level.Error(r.Logger).Log("controller", "PoolReconciler", "message", "failed to get ipaddresspools", "error", err)
		return ctrl.Result{}, err
	}

	var communities metallbv1beta1.CommunityList
	if err := r.List(ctx, &communities, client.InNamespace(r.Namespace)); err != nil {
		level.Error(r.Logger).Log("controller", "PoolReconciler", "message", "failed to get communities", "error", err)
		return ctrl.Result{}, err
	}

	var namespaces corev1.NamespaceList
	if err := r.List(ctx, &namespaces); err != nil {
		level.Error(r.Logger).Log("controller", "ConfigReconciler", "message", "failed to get namespaces", "error", err)
		return ctrl.Result{}, err
	}

	var legacyConfig corev1.ConfigMap
	key := client.ObjectKey{Name: r.LegacyConfigMapName, Namespace: r.Namespace}
	if err := r.Get(ctx, key, &legacyConfig); err != nil && !apierrors.IsNotFound(err) {
		level.Error(r.Logger).Log("controller", "ConfigReconciler", "message", "failed to get the frr configmap", "error", err)
		return ctrl.Result{}, err
	}

	legacyCF, err := conversion.DecodeLegacyCM(legacyConfig)
	if err != nil {
		return ctrl.Result{}, err
	}

	legacyResources, err := conversion.ResourcesFor(legacyCF)
	if err != nil {
		return ctrl.Result{}, err
	}

	resources := config.ClusterResources{
		Pools:              ipAddressPools.Items,
		LegacyAddressPools: addressPools.Items,
		Communities:        communities.Items,
		Namespaces:         namespaces.Items,
	}

	resources = conversion.AddLegacyResources(&resources, &legacyResources)

	level.Debug(r.Logger).Log("controller", "PoolReconciler", "metallb CRs", dumpClusterResources(&resources))

	cfg, err := toConfig(resources, r.ValidateConfig)
	if err != nil {
		configStale.Set(1)
		level.Error(r.Logger).Log("controller", "PoolReconciler", "error", "failed to parse the configuration", "error", err)
		return ctrl.Result{}, nil
	}

	level.Debug(r.Logger).Log("controller", "PoolReconciler", "rendered config", dumpConfig(cfg))
	if reflect.DeepEqual(r.currentConfig, cfg) {
		level.Debug(r.Logger).Log("controller", "PoolReconciler", "event", "configuration did not change, ignoring")
		return ctrl.Result{}, nil
	}

	res := r.Handler(r.Logger, cfg.Pools)
	switch res {
	case SyncStateError:
		updateErrors.Inc()
		configStale.Set(1)
		level.Error(r.Logger).Log("controller", "PoolReconciler", "metallb CRs and Secrets", dumpClusterResources(&resources), "event", "reload failed, retry")
		return ctrl.Result{}, errRetry
	case SyncStateReprocessAll:
		level.Info(r.Logger).Log("controller", "PoolReconciler", "event", "force service reload")
		r.ForceReload()
	case SyncStateErrorNoRetry:
		updateErrors.Inc()
		configStale.Set(1)
		level.Error(r.Logger).Log("controller", "PoolReconciler", "metallb CRs and Secrets", dumpClusterResources(&resources), "event", "reload failed, no retry")
		return ctrl.Result{}, nil
	}

	r.currentConfig = cfg

	configLoaded.Set(1)
	configStale.Set(0)
	level.Info(r.Logger).Log("controller", "PoolReconciler", "event", "config reloaded")
	return ctrl.Result{}, nil
}

func (r *PoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	p := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			return filterNodeEvent(e) && filterNamespaceEvent(e)
		},
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&metallbv1beta1.IPAddressPool{}).
		Watches(&metallbv1beta1.AddressPool{}, &handler.EnqueueRequestForObject{}).
		Watches(&metallbv1beta1.Community{}, &handler.EnqueueRequestForObject{}).
		Watches(&corev1.Namespace{}, &handler.EnqueueRequestForObject{}).
		Watches(&corev1.ConfigMap{}, &handler.EnqueueRequestForObject{}).
		WithEventFilter(p).
		Complete(r)
}
