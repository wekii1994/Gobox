import 'package:flutter_test/flutter_test.dart';
import 'package:gobox/common/updater.dart';
import 'package:gobox/common/utils.dart';

void main() {
  group('AppUpdater.parseChecksum', () {
    test('parses exact match in sha256sums.txt', () {
      const content = '''
e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  Gobox-1.19.9-1-android-arm64-v8a.apk
a1b2c3d4e5f60718293a4b5c6d7e8f90123456789abcdef0123456789abcdef0  other-asset.zip
''';
      expect(
        AppUpdater.parseChecksum(content, 'Gobox-1.19.9-1-android-arm64-v8a.apk'),
        'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
      );
    });

    test('parses match with leading ./ prefix from find sha256sum', () {
      const content = '''
e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  ./Gobox-1.19.9-1-android-arm64-v8a.apk
''';
      expect(
        AppUpdater.parseChecksum(content, 'Gobox-1.19.9-1-android-arm64-v8a.apk'),
        'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
      );
    });

    test('parses binary asterisk prefix and uppercase hash', () {
      const content = '''
E3B0C44298FC1C149AFBF4C8996FB92427AE41E4649B934CA495991B7852B855 *Gobox-1.19.9-1-android-arm64-v8a.apk
''';
      expect(
        AppUpdater.parseChecksum(content, 'Gobox-1.19.9-1-android-arm64-v8a.apk'),
        'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
      );
    });

    test('returns null when asset not found', () {
      const content = '''
e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  different-file.apk
''';
      expect(
        AppUpdater.parseChecksum(content, 'Gobox-1.19.9-1-android-arm64-v8a.apk'),
        isNull,
      );
    });
  });

  group('Utils.compareVersions', () {
    test('handles standard semver and revisions like 1.19.9-1 without throwing FormatException', () {
      // Equal
      expect(utils.compareVersions('1.19.9-1', '1.19.9-1'), 0);
      expect(utils.compareVersions('v1.19.9-1', '1.19.9-1'), 0);

      // Revision comparisons
      expect(utils.compareVersions('1.19.9-2', '1.19.9-1'), greaterThan(0));
      expect(utils.compareVersions('1.19.9-1', '1.19.9-2'), lessThan(0));
      expect(utils.compareVersions('1.19.9-1', '1.19.9'), greaterThan(0));

      // Minor and major comparisons
      expect(utils.compareVersions('1.19.10', '1.19.9-1'), greaterThan(0));
      expect(utils.compareVersions('1.20.0', '1.19.9-1'), greaterThan(0));
      expect(utils.compareVersions('2.0.0', '1.19.9-1'), greaterThan(0));

      // Build metadata
      expect(utils.compareVersions('1.19.9-1+2026112201', '1.19.9-1+2026112101'), greaterThan(0));

      // Pre-release ordering (< release, > lower pre-release)
      expect(utils.compareVersions('1.20.1-pre1', '1.20.1'), lessThan(0));
      expect(utils.compareVersions('1.20.1-pre2', '1.20.1-pre1'), greaterThan(0));
      expect(utils.compareVersions('1.20.1-pre10', '1.20.1-pre2'), greaterThan(0));
      expect(utils.compareVersions('1.20.1-pre1-6', '1.20.1-pre1-5'), greaterThan(0));
      expect(utils.compareVersions('1.20.1-1', '1.20.1-pre1-6'), greaterThan(0));
    });
  });
}
