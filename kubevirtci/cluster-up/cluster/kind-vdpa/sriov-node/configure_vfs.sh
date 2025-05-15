#! /bin/bash

set -ex

function configure_vf_driver() {
  local -r vf_sys_device=$1
  local -r driver=$2

  vf_pci_address=$(basename $vf_sys_device)
  # Check if a VF is bound to a different driver
  if [ -d "$vf_sys_device/driver" ]; then
    vf_bus_pci_device_driver=$(readlink -e $vf_sys_device/driver)
    vf_driver_name=$(basename $vf_bus_pci_device_driver)

    # Check if VF already configured with supported driver
    if [[ $vf_driver_name == $driver ]]; then
      return
    else
      echo "Unbind VF $vf_pci_address from $vf_driver_name driver"
      echo "$vf_pci_address" >> "$vf_bus_pci_device_driver/unbind"
    fi
  fi

  echo "Bind VF $vf_pci_address to $driver driver"
  echo "$driver" >> "$vf_sys_device/driver_override"
  echo "$vf_pci_address" >> "/sys/bus/pci/drivers/$driver/bind"
  echo "" >> "$vf_sys_device/driver_override"

  return 0
}

function create_vdpa_device() {
  local -r vf_sys_device=$1
  local vf_id=$2
  #echo $vf_sys_device # /sys/devices/pci0000:64/0000:64:00.0/0000:65:00.2
  local -r pci_address=$(basename $vf_sys_device)
  #echo $pci_address # 0000:65:00.3
  local -r macaddr=`printf "%02x\n" ${i}`
  vdpa dev add name vdpa:${pci_address} mgmtdev pci/${pci_address} mac 00:11:22:33:44:${macaddr}
  # echo "vdpa dev add name vdpa$:{pci_address} mgmtdev pci/${pci_address} mac 00:11:22:33:44:${macaddr}"
}

function create_vfs() {
  local -r pf_net_device=$1
  local -r vfs_count=$2

  local -r pf_name=$(basename $pf_net_device)
  local -r pf_sys_device=$(readlink -e $pf_net_device)

  local -r sriov_totalvfs_content=$(cat $pf_sys_device/sriov_totalvfs)
  [ $sriov_totalvfs_content -lt $vfs_count ] && \
    echo "FATAL: PF $pf_name, VF's count should be up to sriov_totalvfs: $sriov_totalvfs_content" >&2 && return 1

  echo "Creating $vfs_count VFs on PF $pf_name "
  echo 0 >> "$pf_sys_device/sriov_numvfs"
  echo "$vfs_count" >> "$pf_sys_device/sriov_numvfs"
  sleep 3

  return 0
}

function validate_run_with_sudo() {
  [ "$(id -u)" -ne 0 ] && echo "FATAL: This script requires sudo privileges" >&2 && return 1

  return 0
}

function validate_sysfs_mount_as_rw() {
  local -r sysfs_permissions=$(grep -Po 'sysfs.*\K(ro|rw)' /proc/mounts)
  [ "$sysfs_permissions" != rw ] && echo "FATAL: sysfs is read-only, try to remount as RW" >&2 && return 1

  return 0
}

function ensure_driver_is_loaded() {
  local -r driver_name=$1
  local -r module_name=$2

  if ! grep "$module_name" /proc/modules; then
    if ! modprobe "$driver_name"; then
      echo "FATAL: failed to load $DRIVER kernel module $DRIVER_KMODULE" >&2 && return 1
    fi
  fi

  return 0
}

DRIVER="${DRIVER:-vfio-pci}"
DRIVER_KMODULE="${DRIVER_KMODULE:-vfio_pci}"
VFS_COUNT=2

[ $((VFS_COUNT)) -lt 1 ] && echo "INFO: VFS_COUNT is lower then 1, nothing to do..." && exit 0

validate_run_with_sudo
validate_sysfs_mount_as_rw
ensure_driver_is_loaded $DRIVER $DRIVER_KMODULE

sriov_pfs=( $(find /sys/class/net/*/device/sriov_numvfs) )
[ "${#sriov_pfs[@]}" -eq 0 ] && echo "FATAL: Could not find available sriov PFs" >&2 && exit 1

for pf_name in $sriov_pfs; do
  pf_device=$(dirname "$pf_name")

  echo "Create VF's"
  create_vfs "$pf_device" "$VFS_COUNT"

  echo "Configuring VF's drivers"
  # /sys/class/net/<pf name>/device/virtfn*
  vfs_sys_devices=$(readlink -e $pf_device/virtfn*)
  vf_id=1
  for vf in $vfs_sys_devices; do
    configure_vf_driver "$vf" $DRIVER
    ls -l "$vf/driver"
    #create_vdpa_device "$vf" $vf_id
    vf_id=$((vf_id+1))
  done
done
