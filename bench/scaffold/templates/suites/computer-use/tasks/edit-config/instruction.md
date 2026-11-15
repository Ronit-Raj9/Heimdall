# Move the sync service to the new port

The settings file of our notes sync service is open in the text editor on
the desktop. Change the server's port to 8443 and raise its upload limit
(max_upload_mb) to 50. Leave every other line as it is, and save the file
so the service picks the change up at its next start.
