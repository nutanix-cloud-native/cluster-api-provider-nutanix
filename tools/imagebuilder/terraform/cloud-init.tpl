#cloud-config

# set the hostname
fqdn: ${vmname}

#cloud-config
users:
  - name: ${username}
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    ssh-authorized-keys:
      - ${public_key}

cloud_final_modules:
  - [ssh, always]