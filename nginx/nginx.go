package nginx

import (
	"context"
	"fmt"

	"github.com/layer5io/meshery-adapter-library/adapter"
	"github.com/layer5io/meshery-adapter-library/common"
	adapterconfig "github.com/layer5io/meshery-adapter-library/config"
	"github.com/layer5io/meshery-adapter-library/status"
	internalconfig "github.com/layer5io/meshery-nginx/internal/config"
	"github.com/layer5io/meshkit/logger"
	"github.com/layer5io/meshkit/models"
	"gopkg.in/yaml.v2"
)

// Nginx defines a model for this adapter
type Nginx struct {
	adapter.Adapter // Type Embedded
}

// New initializes nginx handler.
func New(c adapterconfig.Handler, l logger.Handler, kc adapterconfig.Handler) adapter.Handler {
	return &Nginx{
		Adapter: adapter.Adapter{
			Config:            c,
			Log:               l,
			KubeconfigHandler: kc,
		},
	}
}

// ApplyOperation applies the operation on nginx
func (nginx *Nginx) ApplyOperation(ctx context.Context, opReq adapter.OperationRequest, hchan *chan interface{}) error {
	err := nginx.CreateKubeconfigs(opReq.K8sConfigs)
	if err != nil {
		return err
	}
	kubeConfigs := opReq.K8sConfigs
	nginx.SetChannel(hchan);

	operations := make(adapter.Operations)
	err = nginx.Config.GetObject(adapter.OperationsKey, &operations)
	if err != nil {
		return err
	}

	stat := status.Deploying

	e := &adapter.Event{
		Operationid: opReq.OperationID,
		Summary:     status.Deploying,
		Details:     status.None,
	}

	switch opReq.OperationName {
	case internalconfig.NginxOperation:
		go func(hh *Nginx, ee *adapter.Event) {
			version := string(operations[opReq.OperationName].Versions[0])
			if stat, err = hh.installNginx(opReq.IsDeleteOperation, version, opReq.Namespace, kubeConfigs); err != nil {
				e.Summary = fmt.Sprintf("Error while %s NGINX Service Mesh", stat)
				e.Details = err.Error()
				hh.StreamErr(e, err)
				return
			}
			ee.Summary = fmt.Sprintf("NGINX Service Mesh %s successfully", stat)
			ee.Details = fmt.Sprintf("NGINX Service Mesh is now %s.", stat)
			hh.StreamInfo(e)
		}(nginx, e)
	case internalconfig.LabelNamespace:
		go func(hh *Nginx, ee *adapter.Event) {
			err := hh.LoadNamespaceToMesh(opReq.Namespace, opReq.IsDeleteOperation, kubeConfigs)
			operation := "enabled"
			if opReq.IsDeleteOperation {
				operation = "removed"
			}
			if err != nil {
				e.Summary = fmt.Sprintf("Error while labelling %s", opReq.Namespace)
				e.Details = err.Error()
				hh.StreamErr(e, err)
				return
			}
			ee.Summary = fmt.Sprintf("Label updated on %s namespace", opReq.Namespace)
			ee.Details = fmt.Sprintf("NGINX-INJECTION label %s on %s namespace", operation, opReq.Namespace)
			hh.StreamInfo(e)
		}(nginx, e)
	case common.SmiConformanceOperation:
		go func(hh *Nginx, ee *adapter.Event) {
			name := operations[opReq.OperationName].Description
			_, err := hh.RunSMITest(adapter.SMITestOptions{
				Ctx:         context.TODO(),
				OperationID: ee.Operationid,
				Manifest:    string(operations[opReq.OperationName].Templates[0]),
				Namespace:   "meshery",
				Labels:      make(map[string]string),
				Annotations: map[string]string{
					"injector.nsm.nginx.com/auto-inject": "true",
				},
			})
			if err != nil {
				e.Summary = fmt.Sprintf("Error while %s %s test", status.Running, name)
				e.Details = err.Error()
				hh.StreamErr(e, err)
				return
			}
			ee.Summary = fmt.Sprintf("%s test %s successfully", name, status.Completed)
			ee.Details = ""
			hh.StreamInfo(e)
		}(nginx, e)
	case common.BookInfoOperation, common.HTTPBinOperation, common.ImageHubOperation, common.EmojiVotoOperation:
		go func(hh *Nginx, ee *adapter.Event) {
			appName := operations[opReq.OperationName].AdditionalProperties[common.ServiceName]
			stat, err := hh.installSampleApp(opReq.Namespace, opReq.IsDeleteOperation, operations[opReq.OperationName].Templates, kubeConfigs)
			if err != nil {
				e.Summary = fmt.Sprintf("Error while %s %s application", stat, appName)
				e.Details = err.Error()
				hh.StreamErr(e, err)
				return
			}
			ee.Summary = fmt.Sprintf("%s application %s successfully", appName, stat)
			ee.Details = fmt.Sprintf("The %s application is now %s.", appName, stat)
			hh.StreamInfo(e)
		}(nginx, e)
	case common.CustomOperation:
		go func(hh *Nginx, ee *adapter.Event) {
			stat, err := hh.applyCustomOperation(opReq.Namespace, opReq.CustomBody, opReq.IsDeleteOperation, kubeConfigs)
			if err != nil {
				e.Summary = fmt.Sprintf("Error while %s custom operation", stat)
				e.Details = err.Error()
				hh.StreamErr(e, err)
				return
			}
			ee.Summary = fmt.Sprintf("Manifest %s successfully", status.Deployed)
			ee.Details = ""
			hh.StreamInfo(e)
		}(nginx, e)
	default:
		nginx.StreamErr(e, ErrOpInvalid)
	}

	return nil
}

//CreateKubeconfigs creates and writes passed kubeconfig onto the filesystem
func (nginx *Nginx) CreateKubeconfigs(kubeconfigs []string) error {
	var errs = make([]error, 0)
	for _, kubeconfig := range kubeconfigs {
		kconfig := models.Kubeconfig{}
		err := yaml.Unmarshal([]byte(kubeconfig), &kconfig)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		// To have control over what exactly to take in on kubeconfig
		nginx.KubeconfigHandler.SetKey("kind", kconfig.Kind)
		nginx.KubeconfigHandler.SetKey("apiVersion", kconfig.APIVersion)
		nginx.KubeconfigHandler.SetKey("current-context", kconfig.CurrentContext)
		err = nginx.KubeconfigHandler.SetObject("preferences", kconfig.Preferences)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		err = nginx.KubeconfigHandler.SetObject("clusters", kconfig.Clusters)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		err = nginx.KubeconfigHandler.SetObject("users", kconfig.Users)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		err = nginx.KubeconfigHandler.SetObject("contexts", kconfig.Contexts)
		if err != nil {
			errs = append(errs, err)
			continue
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return mergeErrors(errs)
}
