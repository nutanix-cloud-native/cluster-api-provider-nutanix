output "ip_address" {
  value = lookup(nutanix_virtual_machine.vm.nic_list.0.ip_endpoint_list[0], "ip")
}
