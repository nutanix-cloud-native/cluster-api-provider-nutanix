data "nutanix_cluster" "cluster" {
  name = var.cluster_name
}
data "nutanix_subnet" "subnet" {
  subnet_name = var.subnet_name
}

provider "nutanix" {
  username     = var.user
  password     = var.password
  endpoint     = var.endpoint
  insecure     = true
  wait_timeout = 60
}

resource "nutanix_image" "image" {
  name       = "ubuntu-builder"
  source_uri = var.image_url
}

data "template_file" "cloud-init" {
  template = file("${path.module}/cloud-init.tpl")
  vars = {
    "username"   = var.vm_user,
    "vmname"     = var.vm_name,
    "public_key" = file(var.public_key_file_path)
  }
}

resource "nutanix_virtual_machine" "build_vm" {
  name                   = var.vm_name
  enable_cpu_passthrough = true
  cluster_uuid           = data.nutanix_cluster.cluster.id
  num_vcpus_per_socket   = "2"
  num_sockets            = "1"
  memory_size_mib        = 4096
  disk_list {
    disk_size_bytes = 100 * 1024 * 1024 * 1024
    data_source_reference = {
      kind = "image"
      uuid = nutanix_image.image.id
    }
  }

  disk_list {
    disk_size_bytes = 10 * 1024 * 1024 * 1024
    device_properties {
      device_type = "DISK"
      disk_address = {
        "adapter_type" = "SCSI"
        "device_index" = "1"
      }
    }
  }

  disk_list {
    device_properties {
      device_type = "CDROM"
      disk_address = {
        device_index = 0
        adapter_type = "IDE"
      }
    }
  }

  serial_port_list {
    index        = 1
    is_connected = true
  }
  guest_customization_cloud_init_user_data = base64encode(data.template_file.cloud-init.rendered)
  nic_list {
    subnet_uuid = data.nutanix_subnet.subnet.id
  }
}

data "nutanix_virtual_machine" "build_vm_datasource" {
  vm_id = nutanix_virtual_machine.build_vm.id
}

resource "null_resource" "build_os_image" {
  connection {
    type        = "ssh"
    user        = var.vm_user
    private_key = file(var.private_key_file_path)
    host        = data.nutanix_virtual_machine.build_vm_datasource.nic_list.0.ip_endpoint_list[0].ip
  }
  provisioner "file" {
    source      = "${path.module}/scripts/build_os_image.sh"
    destination = "/home/${var.vm_user}/build_os_image.sh"
  }
  provisioner "file" {
    source      = "${path.module}/scripts/install_prerequisites.sh"
    destination = "/home/${var.vm_user}/install_prerequisites.sh"
  }

  provisioner "remote-exec" {
    inline = [
      "ls ~/",
      "chmod u+x ~/*.sh",
      "~/install_prerequisites.sh"
    ]
  }

  provisioner "remote-exec" {
    inline = [
      "export PATH=$PATH:~/.local/bin",
      "PATH=$PATH:~/.local/bin ~/build_os_image.sh"
    ]
  }

  depends_on = [
    data.nutanix_virtual_machine.build_vm_datasource
  ]
}

resource "null_resource" "copy_os_image" {
  connection {
    type        = "ssh"
    user        = var.vm_user
    private_key = file(var.private_key_file_path)
    host        = data.nutanix_virtual_machine.build_vm_datasource.nic_list.0.ip_endpoint_list[0].ip
  }
  provisioner "local-exec" {
    command = "scp -r ${var.vm_user}@${data.nutanix_virtual_machine.build_vm_datasource.nic_list.0.ip_endpoint_list[0].ip}:~/image-builder/images/capi/output ."
  }
}

