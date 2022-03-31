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
    "public_key" = var.public_key
  }
}

resource "nutanix_virtual_machine" "vm" {
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

  provisioner "file" {
    source      = "${path.module}/scripts/build_os_image.sh"
    destination = "/home/ubuntu/build_os_image.sh"
  }
  provisioner "file" {
    source      = "${path.module}/scripts/install_prerequisites.sh"
    destination = "/home/ubuntu/install_prerequisites.sh"
  }
}

