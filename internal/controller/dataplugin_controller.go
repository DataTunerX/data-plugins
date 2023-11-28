/*
Copyright 2023.

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

package controller

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/DataTunerX/data-plugins/pkg/config"
	extensionv1beta1 "github.com/DataTunerX/meta-server/api/extension/v1beta1" // import DataPlugin API
	"github.com/DataTunerX/utility-server/logging"
	"github.com/DataTunerX/utility-server/parser"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// DataPluginReconciler reconciles a DataPlugin object
type DataPluginReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logging.Logger
}

//+kubebuilder:rbac:groups=extension.datatunerx.io,resources=dataplugins,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=extension.datatunerx.io,resources=dataplugins/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=extension.datatunerx.io,resources=dataplugins/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the DataPlugin object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile

// Reconcile reads that state of the cluster for a Dataset object and makes changes based on the state read
// and what is in the Dataset.Spec
func (r *DataPluginReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Log.Info("Reconciling Dataset")

	// Fetch the Dataset instance
	var dataset extensionv1beta1.Dataset
	if err := r.Get(ctx, req.NamespacedName, &dataset); err != nil {
		r.Log.Errorf("unable to fetch Dataset: %v", err)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if dataset.spec.datasetmetadata.subsets is not empty
	if !isSubsetInfoValid(dataset.Spec.DatasetMetadata.DatasetInfo.Subsets) {
		// If subsets are not valid, set dataset status to UNREADY and return
		if dataset.Status == nil {
			dataset.Status = &extensionv1beta1.DatasetStatus{}
		}
		dataset.Status.State = extensionv1beta1.DatasetUnready
		if err := r.Status().Update(ctx, &dataset); err != nil {
			r.Log.Errorf("unable to update Dataset status: %v", err)
			return ctrl.Result{}, err
		}
	} else {
		if dataset.Status == nil {
			dataset.Status = &extensionv1beta1.DatasetStatus{}
		}
		dataset.Status.State = extensionv1beta1.DatasetReady
		if err := r.Status().Update(ctx, &dataset); err != nil {
			r.Log.Errorf("unable to update Dataset status: %v", err)
			return ctrl.Result{}, err
		}
	}

	// Fetch the DataPlugin instance used by the dataset
	var dataPlugin extensionv1beta1.DataPlugin
	if dataset.Spec.DatasetMetadata.Plugin.LoadPlugin {
		dataPluginName := dataset.Spec.DatasetMetadata.Plugin.Name
		if err := r.Get(ctx, types.NamespacedName{
			Namespace: config.GetDatatunerxSystemNamespace(),
			Name:      dataPluginName,
		}, &dataPlugin); err != nil {
			r.Log.Errorf("unable to fetch DataPlugin: %v", err)
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}

		// Merge parameters from DataPlugin and Dataset
		mergedParameters, err := r.mergeParameters(&dataPlugin, &dataset)
		if err != nil {
			return ctrl.Result{}, err
		}

		// Build the path to the plugin YAML file
		pluginPath := filepath.Join("plugins", dataPlugin.Spec.Provider, dataPlugin.Spec.DatasetClass, "plugin.yaml")
		// Apply the plugin YAML file
		if err := r.applyYAML(ctx, pluginPath, &dataset, mergedParameters); err != nil {
			r.Log.Errorf("unable to apply plugin YAML %v: %v", pluginPath, err)
			return ctrl.Result{}, err
		}
	}

	// Successful reconciliation, no need to requeue
	return ctrl.Result{}, nil
}

// isSubsetInfoValid checks if dataset has at least one valid subset with 'train' and 'test' fields
func isSubsetInfoValid(subsets []extensionv1beta1.Subset) bool {
	for _, subset := range subsets {
		// Check if subset has 'train' and 'test' fields
		if subset.Splits.Train.File != "" && subset.Splits.Test.File != "" {
			return true
		}
	}

	// No valid subset found
	return false
}

// Add a new method to merge parameters
func (r *DataPluginReconciler) mergeParameters(dataPlugin *extensionv1beta1.DataPlugin, dataset *extensionv1beta1.Dataset) (map[string]interface{}, error) {
	// Initialize pluginParameters as an empty map
	var pluginParameters map[string]interface{}

	// Check if DataPlugin has non-empty Spec.Parameters
	if dataPlugin.Spec.Parameters != "" {
		// Unmarshal the parameters from DataPlugin
		if err := json.Unmarshal([]byte(dataPlugin.Spec.Parameters), &pluginParameters); err != nil {
			r.Log.Errorf("unable to unmarshal plugin parameters from DataPlugin: %v", err)
			return nil, err
		}
	}

	// Unmarshal the parameters from Dataset
	var datasetParameters map[string]interface{}
	if dataset.Spec.DatasetMetadata.Plugin.Parameters != "" {
		// Unmarshal the parameters from Dataset
		if err := json.Unmarshal([]byte(dataset.Spec.DatasetMetadata.Plugin.Parameters), &datasetParameters); err != nil {
			r.Log.Errorf("unable to unmarshal plugin parameters from Dataset: %v", err)
			return nil, err
		}
	}

	// Merge the parameters, favoring dataset's parameters in case of conflicts
	mergedParameters := make(map[string]interface{})
	for key, value := range pluginParameters {
		mergedParameters[key] = value
	}
	for key, value := range datasetParameters {
		mergedParameters[key] = value
	}

	return mergedParameters, nil
}

// applyYAML reads a YAML file, replaces placeholders with environment variable values, and applies its content to the Kubernetes cluster
func (r *DataPluginReconciler) applyYAML(ctx context.Context, path string, dataset *extensionv1beta1.Dataset, parameters map[string]interface{}) error {

	r.Log.Infof("Applying plugin YAML %v", path)
	// Read the YAML file content
	yamlFile, err := os.ReadFile(path)
	if err != nil {
		r.Log.Errorf("unable to read plugin YAML file: %v", err)
		return err
	}

	// Convert the file content to a string
	yamlStr := string(yamlFile)

	// Replace placeholders with environment variable values and run-time parameters defined in the dataset
	replacedYamlStr, err := r.replacePlaceholders(yamlStr, parameters, dataset)
	if err != nil {
		r.Log.Errorf("unable to replace placeholders in YAML: %v", err)
		return err
	}

	// Convert the updated YAML string back to a byte slice
	yamlFile = []byte(replacedYamlStr)

	// Decode the YAML into an unstructured.Unstructured object
	decUnstructured := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
	unstructuredObj := &unstructured.Unstructured{}
	_, _, err = decUnstructured.Decode(yamlFile, nil, unstructuredObj)
	if err != nil {
		r.Log.Errorf("unable to decode YAML into Unstructured: %v", err)
		return err
	}

	// Set the namespace and owner reference
	unstructuredObj.SetNamespace(dataset.GetNamespace())
	if err := ctrl.SetControllerReference(dataset, unstructuredObj, r.Scheme); err != nil {
		r.Log.Errorf("unable to set controller reference: %v", err)
		return err
	}

	// Apply the unstructured object using the client
	if err := r.applyClient(ctx, unstructuredObj); err != nil {
		r.Log.Errorf("unable to apply Unstructured object: %v", err)
		return err
	}

	return nil
}

// replacePlaceholders replaces a specific placeholder in the YAML file with the value from an environment variable
func (r *DataPluginReconciler) replacePlaceholders(yamlStr string, parameters map[string]interface{}, dataset *extensionv1beta1.Dataset) (string, error) {

	// Add the required fields defined in the plugin standard to parameters
	baseUrl := config.GetCompleteNotifyURL()
	parameters["CompleteNotifyUrl"] = "http://patch-k8s-server." + config.GetDatatunerxSystemNamespace() + ".svc.cluster.local" + baseUrl + dataset.Namespace + "/datasets/" + dataset.Name

	// Replace the value in template yaml
	replacedYamlStr, err := parser.ReplaceTemplate(yamlStr, parameters)
	if err != nil {
		r.Log.Errorf("unable to replace placeholders in YAML: %v", err)
		return "", err
	}

	return replacedYamlStr, nil
}

// applyClient applies or updates the given unstructured object in the cluster using the client
func (r *DataPluginReconciler) applyClient(ctx context.Context, obj *unstructured.Unstructured) error {
	// First, try to get the resource, if it exists, update it
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(obj.GroupVersionKind())
	err := r.Get(ctx, client.ObjectKey{Name: obj.GetName(), Namespace: obj.GetNamespace()}, existing)
	if err != nil && !errors.IsNotFound(err) {
		r.Log.Errorf("unable to get existing resource: %v", err)
		return err
	}

	if err == nil {
		// Resource exists, update it
		obj.SetResourceVersion(existing.GetResourceVersion())
		if err := r.Update(ctx, obj); err != nil {
			r.Log.Errorf("unable to update resource: %v", err)
			return err
		}
		r.Log.Info("resource updated successfully")
	} else {
		// Resource does not exist, create it
		if err := r.Create(ctx, obj); err != nil {
			r.Log.Errorf("unable to create resource: %v", err)
			return err
		}
		r.Log.Info("resource created successfully")
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DataPluginReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&extensionv1beta1.Dataset{}).
		WithEventFilter(predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldDataset, ok := e.ObjectOld.(*extensionv1beta1.Dataset)
				if !ok {
					return false
				}

				newDataset, ok := e.ObjectNew.(*extensionv1beta1.Dataset)
				if !ok {
					return false
				}
				// Only when dataset.spec.datasetmetadata.plugin changes, it returns true, indicating that it needs to be processed
				return oldDataset.Spec.DatasetMetadata.Plugin != newDataset.Spec.DatasetMetadata.Plugin
			},
		}).
		Complete(r)
}
