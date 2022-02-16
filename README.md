# cluster-api-provider-nutanix


# Welcome to your new service
We've created an empty service structure to show you the setup and workflow with Canaveral.

### Directory Structure
The top level directory of your repository should be set up like this:
  1. `README.md`: this file contains a textual description of the repository.
  2. `.circleci/`: this directory contains CircleCI's `config.yml` file.
  3. `hooks/`: this directory, if present, can contain *ad hoc* scripts that customize your build.
  4. `package/`:  add your `Dockerfile` under `package/docker/` to build a docker image.  (Note:  You can refer to files and folders directly in your `Dockerfile` because all files and folders under `services/` will be copied into the same folder as the `Dockerfile` during build.)
  5. `services/`: this directory should have a subdirectory for each `service`, *e.g.* `services/my-service/`.  Each subdirectory (often there is only one) would contain the definition (source and tests) for the service.
  6. `blueprint.json`: this file, if present, contains instructions for Canaveral to deploy the service.

### Build
Canaveral uses CircleCI for building, packaging, and alerting its Deployment Engine. Your repository should have been registered with CircleCI when it was provisioned.  Here are some additional steps you should follow to ensure proper builds:

##### Ensure `.circleci/config.yml` has the correct variables (docker image only)
  1. Specify your preferred `CANAVERAL_BUILD_SYSTEM` (default is noop)
  2. Specify your preferred `CANAVERAL_PACKAGE_TOOLS` (use "docker" if deploying a docker image, use "noop" if no packaging is needed)
  3. **[OPTIONAL]** Specify the target `DOCKERFILE_NAME` to use  (default is Dockerfile)

You'll be able to monitor the build at [circleci.canaveral-corp.us-west-2.aws](https://circleci.canaveral-corp.us-west-2.aws/)

### Deployment
To use Canaveral for deployment, `blueprint.json` should be placed at the top level of the repo.  Spec for the blueprint can be found at [Canaveral Blueprint Spec](https://confluence.eng.nutanix.com:8443/x/5kbdBQ).

__Questions, issues or suggestions? Reach us at https://nutanix.slack.com/messages/canaveral-onboarding/.__
