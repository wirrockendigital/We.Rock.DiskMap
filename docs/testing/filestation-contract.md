# File Station integration

DiskMap can request that DSM File Station open a selected folder. When a file is selected, the app requests its parent folder; automatic highlighting of the file is not implied.

The request uses the current authenticated DSM administrator session and a validated shared-folder path. DiskMap does not infer access from the saved scan result alone. If the session expires or DSM cannot open the target, the app reports the limitation without treating the request as a completed navigation.

This integration is qualified for the DSM build recorded in the app's compatibility information. Other DSM builds require their own verification.

The recorded VirtualDSM 7.4.1 build 90080 check confirmed folder and file-parent navigation in four isolated cases. Logging out blocked a further launch. The check did not qualify automatic file highlighting or every DSM version.
