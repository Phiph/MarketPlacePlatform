# Promise Template

This Promise was generated with:

```
kratix init promise container  --group demo.kratix.io --kind Container
```

## Updating API properties

To update the Promise API, you can use the `kratix update api` command:

```
kratix update api --property name:string --property region- --kind Container
```

## Updating Workflows

To add workflow containers, you can use the `kratix add container` command:

```
kratix add container resource/configure/pipeline0 --image syntasso/postgres-resource:v1.0.0
```

For this Stack to work as intended, ensure the referenced Secret is present in the namespace where this Workflow runs.
This secret can be populated manually or via a new container in the `Workflow.Promise.Configure` field if it is common to all resources or the `Workflow.Resource.Configure` if it is unique per request.

## Updating Dependencies

TBD
