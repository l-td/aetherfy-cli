"""Print the control plane's response schemas as JSON, for test/cpschemas.

Run with the control plane's own Python (its requirements installed), from the
root of an aetherfy-control-plane checkout. It imports the FastAPI app, because
only the app's own `openapi()` knows which model each route answers with.

Output: {"operations": {"<METHOD> <path> <status>": <schema or null>},
         "schemas": {<component name>: <schema>}}

Every 2xx answer of every route in the app's document is listed, keyed the way
the CLI's pairs name it. A 2xx with no JSON body (a 204) is null; a route that
declares no model is {} (any JSON), which is exactly what the pairs test must
see to say "this route has no declared shape".

Prose is dropped (title, description, examples, default) so that rewording a
docstring does not churn the committed snapshot. It is dropped from SCHEMA
objects only: a property NAMED "description" or "title" is a wire field, and the
walk below never treats a `properties` map as a schema.
"""
import json
import os
import sys

sys.path.insert(0, os.getcwd())

from main import app  # noqa: E402  (the path must be set first)

PROSE = {"title", "description", "examples", "example", "default"}
SCHEMA_LISTS = ("anyOf", "oneOf", "allOf", "prefixItems")
SCHEMA_VALUES = ("items", "additionalProperties", "not", "contains")


def clean(schema):
    if not isinstance(schema, dict):
        return schema  # `additionalProperties: true` and the like
    out = {}
    for key, value in schema.items():
        if key in PROSE:
            continue
        if key == "properties":
            out[key] = {name: clean(sub) for name, sub in value.items()}
        elif key in SCHEMA_LISTS:
            out[key] = [clean(sub) for sub in value]
        elif key in SCHEMA_VALUES:
            out[key] = clean(value)
        elif key == "$defs":
            out[key] = {name: clean(sub) for name, sub in value.items()}
        else:
            out[key] = value
    return out


def main():
    doc = app.openapi()
    operations = {}
    for path, methods in doc.get("paths", {}).items():
        for method, op in methods.items():
            for status, answer in (op.get("responses") or {}).items():
                if not str(status).startswith("2"):
                    continue
                body = ((answer.get("content") or {}).get("application/json") or {})
                schema = body.get("schema") if "schema" in body else None
                operations[f"{method.upper()} {path} {status}"] = clean(schema) if schema is not None else None
    schemas = {name: clean(s) for name, s in (doc.get("components") or {}).get("schemas", {}).items()}
    json.dump({"operations": operations, "schemas": schemas}, sys.stdout, sort_keys=True)


main()
