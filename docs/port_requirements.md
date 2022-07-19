# Port requirements

CAPX uses the ports documented below to create workload clusters. 

**Note**: This page only documents the ports specifically required by CAPX and does not provide the full overview of all ports required in the CAPI framework.

## Management cluster

|Source            |Destination         |Protocol  |Port |Description                                                                                 |
|------------------|--------------------|----------|-----|--------------------------------------------------------------------------------------------|
|Management cluster|External Registries |TCP       |443  |Pull container images from [CAPX public registries](#public-registries-used-when-using-capx)|
|Management cluster|Prism Central       |TCP       |9440 |Management cluster communication to Prism Central                                           |

## Public registries used when using CAPX

|Registry name|
|-------------|
|ghcr.io      |
