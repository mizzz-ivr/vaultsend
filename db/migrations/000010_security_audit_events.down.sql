DROP TRIGGER IF EXISTS security_audit_events_reject_truncate ON security_audit_events;
DROP TRIGGER IF EXISTS security_audit_events_reject_update_delete ON security_audit_events;
DROP FUNCTION IF EXISTS reject_security_audit_event_mutation();
DROP TABLE IF EXISTS security_audit_events;
