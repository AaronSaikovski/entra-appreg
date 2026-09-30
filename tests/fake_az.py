"""Azure CLI stand-in used only by offline process-level tests."""

import json
import os
from pathlib import Path
import sys


Path(os.environ["AUTH_MARKER"]).touch()
if record_path := os.environ.get("TEST_AZ_ARGUMENTS"):
    with Path(record_path).open("a", encoding="utf-8") as record:
        record.write(json.dumps(sys.argv[1:]) + "\n")
if os.environ.get("TEST_AZ_SUCCESS") != "1":
    sys.exit(1)

if sys.argv[1:3] == ["account", "show"]:
    print(os.environ.get("TEST_AZ_TENANT_OUTPUT", "11111111-1111-1111-1111-111111111111"))
elif sys.argv[1:3] == ["account", "get-access-token"]:
    token_output = os.environ.get("TEST_AZ_TOKEN_OUTPUT")
    if token_output is not None:
        print(token_output)
    else:
        print(json.dumps({
            "accessToken": "offline-test-token",
            "expires_on": 4102444800,
            "expiresOn": "2100-01-01 00:00:00.000000",
            "tokenType": "Bearer",
        }))
else:
    sys.exit(1)
