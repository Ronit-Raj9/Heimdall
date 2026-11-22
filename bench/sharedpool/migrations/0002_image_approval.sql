-- How an image request was approved (lane Y10): "automatic" when the server
-- approved a digest-pinned image within the size limit for an org whose
-- shared worker runs agents under gVisor, "admin" when a server admin did.
-- Empty for older rows and for pending or rejected requests.
ALTER TABLE image_requests ADD COLUMN approval TEXT NOT NULL DEFAULT '';
