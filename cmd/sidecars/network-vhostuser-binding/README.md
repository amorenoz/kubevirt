# KubeVirt network vhost-user binding plugin

## Summary

vhost-user network binding plugin configures VMs to consume a vhost-user interface for fast
user-space virtio datapath. 

# How to use

Register the `vhostuser` binding plugin with its sidecar image:

```yaml
apiVersion: kubevirt.io/v1
kind: KubeVirt
metadata:
  name: kubevirt
  namespace: kubevirt
spec:
  configuration:
    network:
      binding:
        vhostuser:
          sidecarImage: registry:5000/kubevirt/network-vhostuser-binding:devel
  ...
```

In the VM spec, set interface to use `vhostuser` binding plugin:

```yaml
apiVersion: kubevirt.io/v1
kind: VirtualMachineInstance
metadata:
  name: vmi-vhostuser
spec:
  domain:
    devices:
      interfaces:
      - name: vhostuser
        binding:
          name: vhostuser
  ...
  networks:
  - name: vhostuser-net
    pod: {}
  ...
```
