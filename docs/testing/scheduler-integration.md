# Scheduled scans and the shared job queue

DiskMap runs scans through one shared job manager. A scan started from the app and a scan started through a configured DSM task for the same profile can join the same accepted job instead of creating duplicate work. Other profiles wait in the queue.

The profile settings used by a job are captured when the job is accepted. Pausing, resuming, and cancelling are explicit actions. Closing a browser window or a command-line waiter does not cancel a running scan.

A partial or failed scan remains distinct from a completed result. The previous usable scan result stays available until a newer validated result is published. Configure and review DSM tasks with administrator privileges, and check the resulting job status in DiskMap.

On the recorded VirtualDSM 7.4.1 build 90080, two DSM tasks joined one app-started job; they did not create competing scans. Cancellation retained the previous complete result. This observation is specific to that tested build.
