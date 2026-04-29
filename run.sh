#!/bin/bash
podman run  \
	--user root \
       	--cap-add SYS_ADMIN \
	--device /dev/fuse  \
	--security-opt seccomp=unconfined \
	--security-opt label=disable \
	--secret aws_access_key,type=env,target=AWS_ACCESS_KEY_ID \
	--secret aws_secret_key,type=env,target=AWS_SECRET_ACCESS_KEY \
	-e AWS_REGION=us-east-1 \
	-e BUILDAH_ISOLATION=chroot \
	--network host \
	-it \
	-v ./tests/:/data:Z \
	image-build:test bash
