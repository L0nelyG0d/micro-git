# incidents/

One directory per incident, written by the pipeline (git-ignored except this file).

    incidents/<id>/
      alert.json               the Alertmanager alert (listener)
                               (audit.jsonl first lines: alert_received, then config = effective AUTO_EXECUTE; default 0 = dry-run)
      evidence/app_runtime.json  FAULT_ENDPOINT_500 value + image tag of the running app container (only those)
      evidence/*.json          redacted, size-capped read-only query results
      evidence-manifest.json   per source: query, time, sha256, bytes, status (failed sources listed)
      responder-raw.txt        raw model output;  response.json = parsed answer
      decision.json            policy-check verdict + reasons
      action.lock              present once the single automatic action was claimed
      action-output.txt        output of the executed allowlisted command
      escalation-packet.json   written when a human must decide (listener may write escalation.json)
      audit.jsonl              append-only: {ts, actor, kind, detail, decision}
