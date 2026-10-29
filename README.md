# We.Rock.DiskMap — Storage Analysis for Synology NAS

We.Rock.DiskMap is a disk usage analyzer for Synology DSM. It helps NAS administrators understand where storage is used by scanning file and folder metadata, then turning the results into interactive disk maps and a searchable file list. Use it to find large files, explore shared folders, and see how storage is distributed across a NAS.

## Explore your storage

- **See space at a glance.** A treemap and a radial map show the relative sizes of folders and files. Move through the folder tree to inspect any area in detail.
- **Find what matters.** Search and filter the file list by name, path, extension, file type, and size. Sort results and include subfolders when you need a wider view.
- **Choose the right scope.** Scan the NAS, a volume, a shared folder, or a specific folder. Refresh a selected area when you want a newer result.
- **Understand the result.** DiskMap shows when a scan was captured and identifies partial coverage, so missing access is not mistaken for empty storage.
- **Continue in DSM.** Open a selected folder in File Station or copy its path. File actions use your current DSM permissions.

The app is available in English and German. We.Rock.DiskMap is for **Synology DSM administrators only**. Scan results are shared among DSM administrators; personal display preferences remain individual.

## Beta software

We.Rock.DiskMap is in beta. Test the app on **VirtualDSM or a non-production Synology DSM system first** before using it on a production NAS.

The software is provided **as is**, without warranty. To the extent permitted by applicable law, its copyright holders and contributors are not liable for damages arising from its use or inability to use it. See [GPL-3.0-only](LICENSE), sections 15 and 16, for the full terms.

## Install on Synology DSM

1. Obtain a We.Rock.DiskMap `.spk` package built for your DSM version and NAS architecture.
2. Sign in to DSM as an administrator and open **Package Center → Manual Install**.
3. Select the package, review the package information, and complete installation.
4. In **Control Panel → Shared Folder → Edit → Permissions**, grant the internal system user `we-rock-diskmap` **at least read access to every shared folder you want to scan**. Set this permission for each share separately.
5. Open **We.Rock.DiskMap** from the DSM main menu.

This repository currently provides source code rather than a general SPK download. Use a package supplied for your specific DSM build and NAS model.

Read access to a shared folder may not cover every subfolder. If a scan reports partial coverage, check the relevant DSM permissions before refreshing that area.

## Use DiskMap

1. Select the NAS, a volume, a shared folder, or a folder in the left tree.
2. Select **Scan** or **Refresh** next to the chosen path. Progress appears in the app; the scan continues if you close its window.
3. Explore the result in the treemap, radial map, and file list. Select an item for details, or open a folder to navigate deeper.
4. Use search, filters, and sorting to locate files. Check the capture time and coverage indicator when interpreting sizes.

DiskMap reads file metadata during a scan. Its maps and lists show the saved scan result, which you can refresh when the files on the NAS change. A displayed file size does not necessarily equal the space that deleting the file would free.

## License

We.Rock.DiskMap is licensed under **GPL-3.0-only**. See [LICENSE](LICENSE).
