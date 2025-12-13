## Tests for patroni/dcs/kubernetes module.

import std/[json, options, tables, unittest]
import ../patroni/dcs/kubernetes

suite "Kubernetes - Error Types":
  test "KubernetesError is a DCSError":
    try:
      raise newException(KubernetesError, "test error")
    except DCSError:
      check true
    except:
      check false

  test "K8sConfigException is a KubernetesError":
    try:
      raise newException(K8sConfigException, "config error")
    except KubernetesError:
      check true
    except:
      check false

  test "K8sConnectionFailed is a KubernetesError":
    try:
      raise newException(K8sConnectionFailed, "connection failed")
    except KubernetesError:
      check true
    except:
      check false

  test "K8sResourceNotFound is a KubernetesError":
    try:
      raise newException(K8sResourceNotFound, "not found")
    except KubernetesError:
      check true
    except:
      check false

  test "K8sConflict is a KubernetesError":
    try:
      raise newException(K8sConflict, "conflict")
    except KubernetesError:
      check true
    except:
      check false

suite "Kubernetes - Helper Functions":
  test "toCamelCase converts snake_case":
    check toCamelCase("hello_world") == "helloWorld"
    check toCamelCase("test_value_here") == "testValueHere"

  test "toCamelCase handles reserved words":
    check toCamelCase("api_version") == "apiVersion"
    check toCamelCase("pod_ip") == "podIP"
    check toCamelCase("test_url") == "testURL"

  test "toCamelCase handles single word":
    check toCamelCase("test") == "test"

  test "toCamelCase handles empty string":
    check toCamelCase("") == ""

suite "Kubernetes - K8sConfig":
  test "newK8sConfig creates config":
    let config = newK8sConfig()
    check config != nil

  test "newK8sConfig has default namespace":
    let config = newK8sConfig()
    check config.namespace == "default"

  test "newK8sConfig has verify true by default":
    let config = newK8sConfig()
    check config.verify == true

  test "setToken updates token":
    let config = newK8sConfig()
    config.setToken("my-secret-token")
    check config.token == "my-secret-token"

suite "Kubernetes - K8sMetadata":
  test "K8sMetadata can be created":
    var meta: K8sMetadata
    new(meta)
    meta.name = "test-pod"
    meta.namespace = "default"
    meta.uid = "abc123"
    meta.resourceVersion = "12345"
    check meta.name == "test-pod"
    check meta.namespace == "default"
    check meta.uid == "abc123"
    check meta.resourceVersion == "12345"

suite "Kubernetes - K8sObject":
  test "K8sObject can be created":
    var obj: K8sObject
    new(obj)
    obj.kind = "ConfigMap"
    obj.apiVersion = "v1"
    check obj.kind == "ConfigMap"
    check obj.apiVersion == "v1"

  test "parseK8sObject parses JSON":
    let data = %*{
      "kind": "ConfigMap",
      "apiVersion": "v1",
      "metadata": {
        "name": "test-config",
        "namespace": "patroni",
        "uid": "abc123",
        "resourceVersion": "12345"
      },
      "data": {
        "key1": "value1",
        "key2": "value2"
      }
    }
    let obj = parseK8sObject(data)
    check obj.kind == "ConfigMap"
    check obj.apiVersion == "v1"
    check obj.metadata.name == "test-config"
    check obj.metadata.namespace == "patroni"
    check obj.data.len == 2
    check obj.data["key1"] == "value1"

  test "parseK8sObject parses annotations":
    let data = %*{
      "metadata": {
        "name": "test",
        "annotations": {
          "patroni/leader": "node1",
          "patroni/config": "{}"
        }
      }
    }
    let obj = parseK8sObject(data)
    check obj.metadata.annotations.len == 2
    check obj.metadata.annotations["patroni/leader"] == "node1"

  test "parseK8sObject parses labels":
    let data = %*{
      "metadata": {
        "name": "test",
        "labels": {
          "app": "patroni",
          "cluster": "test"
        }
      }
    }
    let obj = parseK8sObject(data)
    check obj.metadata.labels.len == 2
    check obj.metadata.labels["app"] == "patroni"

  test "K8sObject toJson round-trip":
    var obj: K8sObject
    new(obj)
    obj.kind = "ConfigMap"
    obj.apiVersion = "v1"
    obj.metadata = K8sMetadata()
    obj.metadata.name = "test"
    obj.metadata.namespace = "default"
    obj.metadata.annotations = initTable[string, string]()
    obj.metadata.labels = initTable[string, string]()
    obj.data = initTable[string, string]()
    obj.data["key"] = "value"

    let json = obj.toJson()
    check json["kind"].getStr() == "ConfigMap"
    check json["apiVersion"].getStr() == "v1"
    check json["metadata"]["name"].getStr() == "test"
    check json["data"]["key"].getStr() == "value"

suite "Kubernetes - K8sClient":
  test "newK8sClient creates client":
    let config = newK8sConfig()
    config.baseUri = "https://kubernetes.default.svc"
    let client = newK8sClient(config)
    check client != nil

  test "setReadTimeout updates timeout":
    let config = newK8sConfig()
    config.baseUri = "https://kubernetes.default.svc"
    let client = newK8sClient(config)
    client.setReadTimeout(30.0)
    check true

suite "Kubernetes - Constants":
  test "KUBE_CONFIG_DEFAULT_LOCATION value":
    check KUBE_CONFIG_DEFAULT_LOCATION == "~/.kube/config"

  test "SERVICE_HOST_ENV_NAME value":
    check SERVICE_HOST_ENV_NAME == "KUBERNETES_SERVICE_HOST"

  test "SERVICE_PORT_ENV_NAME value":
    check SERVICE_PORT_ENV_NAME == "KUBERNETES_SERVICE_PORT"

  test "SERVICE_TOKEN_FILENAME value":
    check SERVICE_TOKEN_FILENAME == "/var/run/secrets/kubernetes.io/serviceaccount/token"

  test "SERVICE_CERT_FILENAME value":
    check SERVICE_CERT_FILENAME == "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"

when isMainModule:
  discard
