-- Phase 1 migration: FreeSWITCH no longer binds the public SIP ports; those
-- belong to OpenSIPS now. Move the Sofia profile bindings onto 15060/15061/15080
-- and refresh the publicly advertised address.
--
-- Idempotent — safe to re-run.

UPDATE global_variables SET variable_value = '15060' WHERE variable_name = 'internal_sip_port';
UPDATE global_variables SET variable_value = '15061' WHERE variable_name = 'internal_tls_port';
UPDATE global_variables SET variable_value = '15080' WHERE variable_name = 'external_sip_port';
UPDATE global_variables SET variable_value = '15081' WHERE variable_name = 'external_tls_port';
UPDATE global_variables SET variable_value = '43.240.117.122'
    WHERE variable_name IN ('external_rtp_ip', 'external_sip_ip');

UPDATE config_sofia_profile_parameters SET param_value = '15060'
    WHERE param_name = 'sip-port'
      AND parent_id = (SELECT id FROM config_sofia_profiles WHERE param_name = 'internal');
UPDATE config_sofia_profile_parameters SET param_value = '15061'
    WHERE param_name = 'tls-sip-port'
      AND parent_id = (SELECT id FROM config_sofia_profiles WHERE param_name = 'internal');
UPDATE config_sofia_profile_parameters SET param_value = '15080'
    WHERE param_name = 'sip-port'
      AND parent_id = (SELECT id FROM config_sofia_profiles WHERE param_name = 'external');
UPDATE config_sofia_profile_parameters SET param_value = '43.240.117.122'
    WHERE param_name IN ('ext-rtp-ip', 'ext-sip-ip');
