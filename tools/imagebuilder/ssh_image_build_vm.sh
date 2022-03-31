pushd ./terraform
ssh terraform@$(terraform output -raw public_ip) -i ../tf-cloud-init
popd