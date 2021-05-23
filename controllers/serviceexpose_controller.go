/*
Copyright 2021.

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

	"github.com/go-logr/logr"
	"github.com/juju/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	serviceexposerv1alpha1 "github.com/takumakume/service-exposer/api/v1alpha1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const serviceExposeFinalizer = "service-exposer.github.io/finalizer"

// ServiceExposeReconciler reconciles a ServiceExpose object
type ServiceExposeReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=service-exposer.takumakume.github.io,resources=serviceexposes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=service-exposer.takumakume.github.io,resources=serviceexposes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=service-exposer.takumakume.github.io,resources=serviceexposes/finalizers,verbs=update
//+kubebuilder:rbac:groups=networking.k8s.io/v1,resources=ingress,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ServiceExpose object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *ServiceExposeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	reqLogger := r.Log.WithValues("serviceexpose", req.NamespacedName)

	// your logic here

	exp := &serviceexposerv1alpha1.ServiceExpose{}
	err := r.Get(ctx, req.NamespacedName, exp)

	if err != nil {
		// cleanup-on-deletion: https://sdk.operatorframework.io/docs/building-operators/golang/advanced-topics/#handle-cleanup-on-deletion
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			reqLogger.Info("ServiceExpose resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}

		// Error reading the object - requeue the request.
		reqLogger.Error(err, "Failed to get ServiceExpose.")
		return ctrl.Result{}, err
	}

	isServiceExposeMarkedToBeDeleted := exp.GetDeletionTimestamp() != nil
	if isServiceExposeMarkedToBeDeleted {
		if controllerutil.ContainsFinalizer(exp, serviceExposeFinalizer) {
			// Run finalization logic for serviceExposeFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			if err := r.finalizeServiceExpose(reqLogger, exp); err != nil {
				return ctrl.Result{}, err
			}

			// Remove serviceExposeFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			controllerutil.RemoveFinalizer(exp, serviceExposeFinalizer)
			err := r.Update(ctx, exp)
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		// Add finalizer for this CR
		if !controllerutil.ContainsFinalizer(exp, serviceExposeFinalizer) {
			controllerutil.AddFinalizer(exp, serviceExposeFinalizer)
			err = r.Update(ctx, exp)
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	if exp.Spec.Backend.Service.Name != "" && exp.Spec.Backend.Resource.Name != "" {
		return ctrl.Result{}, errors.New("Either Spec.Backend.Service and Spec.Backend.Resource")
	}

	ingressName := ""
	if exp.Spec.Backend.Service.Name != "" && exp.Spec.Backend.Resource.Name != "" {
		return ctrl.Result{}, errors.New("Either Spec.Backend.Service and Spec.Backend.Resource")
	} else if exp.Spec.Backend.Service.Name != "" {
		ingressName = exp.Spec.Backend.Service.Name
	} else if exp.Spec.Backend.Resource.Name != "" {
		ingressName = exp.Spec.Backend.Resource.Name
	} else {
		return ctrl.Result{}, errors.New("Empty Spec.Backend.Service and Spec.Backend.Resource")
	}

	// create ingress if not exists
	found := &networkingv1.Ingress{}
	err = r.Get(ctx, types.NamespacedName{Name: ingressName, Namespace: exp.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		ing := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ingressName,
				Namespace: exp.Namespace,
			},
			Spec: networkingv1.IngressSpec{},
		}

		_ = ctrl.SetControllerReference(exp, ing, r.Scheme)

		//log.Info("Creating a new Ingress", "Ingress.Namespace", ing.Namespace, "Ingress.Name", ing.Name)
		err = r.Create(ctx, ing)
		if err != nil {
			//log.Error(err, "Failed to create new Ingress", "Ingress.Namespace", ing.Namespace, "Ingress.Name", ing.Name)
			return ctrl.Result{}, err
		}

		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		//log.Error(err, "Failed to get Ingress")
		return ctrl.Result{}, err
	} else if found != nil {
		// update ingress if diff
		needUpdate := false
		if len(found.Spec.Rules) == 1 {
			if len(found.Spec.Rules[0].HTTP.Paths) == 1 {
				if !reflect.DeepEqual(found.Spec.Rules[0].HTTP.Paths[0].Backend, exp.Spec.Backend) {
					needUpdate = true
				}
				if found.Spec.Rules[0].HTTP.Paths[0].Path != exp.Spec.Path {
					needUpdate = true
				}
				if found.Spec.Rules[0].HTTP.Paths[0].PathType != exp.Spec.PathType {
					needUpdate = true
				}
			} else {
				needUpdate = true
			}
		} else {
			needUpdate = true
		}
		if (len(found.Spec.TLS) > 0) != exp.Spec.TLSEnabled {
			needUpdate = true
		}
		if exp.Spec.TLSEnabled && len(found.Spec.TLS) > 0 && (found.Spec.TLS[0].SecretName != exp.Spec.TLSSecretName) {
			needUpdate = true
		}
		if !reflect.DeepEqual(found.Annotations, exp.Annotations) {
			needUpdate = true
		}

		if needUpdate {
			//log.Info("Update Ingress", "Ingress.Namespace", found.Namespace, "Ingress.Name", found.Name)
			// TODO: Update ingress
		}

		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

func (r *ServiceExposeReconciler) finalizeServiceExpose(reqLogger logr.Logger, m *serviceexposerv1alpha1.ServiceExpose) error {
	// TODO(user): Add the cleanup steps that the operator
	// needs to do before the CR can be deleted. Examples
	// of finalizers include performing backups and deleting
	// resources that are not owned by this CR, like a PVC.
	//reqLogger.Info("Successfully finalized ServiceExpose")

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceExposeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&serviceexposerv1alpha1.ServiceExpose{}).
		Complete(r)
}
