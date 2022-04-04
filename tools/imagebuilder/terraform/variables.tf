variable "password" {
  type = string
}
variable "endpoint" {
  type = string
}
variable "user" {
  type = string
}

variable "image_url" {
  default = "https://cloud-images.ubuntu.com/focal/current/focal-server-cloudimg-amd64.img"
}

variable "cluster_name" {
  type = string
}
variable "vm_name" {
  type = string
}
variable "vm_user" {
  type = string
}
variable "subnet_name" {
  type = string
}
variable "public_key_file_path" {
  type = string
}
variable "private_key_file_path" {
  type = string
}
