import 'package:flutter_test/flutter_test.dart';
import 'package:gobox/enum/enum.dart';
import 'package:gobox/models/models.dart';
import 'package:gobox/plugins/service.dart';
import 'package:gobox/providers/state.dart';
import 'package:gobox/state.dart';

Proxy _proxy(String name, {String? now}) =>
    Proxy(name: name, type: 'Shadowsocks', now: now);

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  group('resolvePhysicalOutboundTitle', () {
    const fallback = 'Gobox';

    test('Mode.direct 模式下返回 Direct', () {
      final title = resolvePhysicalOutboundTitle(
        mode: Mode.direct,
        groups: [],
        selectedMap: const {},
        fallbackLabel: fallback,
      );
      expect(title, 'Direct');
    });

    test('单层 Selector 选中普通代理节点时解析为物理节点名', () {
      final groups = [
        Group(
          type: GroupType.Selector,
          name: 'PROXY',
          now: '🇩🇪 德国·移联01',
          all: [_proxy('🇩🇪 德国·移联01'), _proxy('🇸🇬 新加坡·移联02')],
        ),
      ];

      final title = resolvePhysicalOutboundTitle(
        mode: Mode.rule,
        groups: groups,
        selectedMap: const {'PROXY': '🇩🇪 德国·移联01'},
        mainGroupName: 'PROXY',
        fallbackLabel: fallback,
      );
      expect(title, '🇩🇪 德国·移联01');
    });

    test('嵌套自动策略组（URL-Test）自动切换时能正确穿透解析到活跃叶子节点', () {
      // 场景：主策略组 PROXY 指向自动策略组 Auto
      // 初始状态：Auto 选中 🇩🇪 德国·移联01
      final groupsGermany = [
        Group(
          type: GroupType.Selector,
          name: 'PROXY',
          now: 'Auto',
          all: [_proxy('Auto')],
        ),
        Group(
          type: GroupType.URLTest,
          name: 'Auto',
          now: '🇩🇪 德国·移联01',
          all: [_proxy('🇩🇪 德国·移联01'), _proxy('🇸🇬 新加坡·移联02')],
        ),
      ];

      final titleBefore = resolvePhysicalOutboundTitle(
        mode: Mode.rule,
        groups: groupsGermany,
        selectedMap: const {'PROXY': 'Auto'},
        mainGroupName: 'PROXY',
        fallbackLabel: fallback,
      );
      expect(titleBefore, '🇩🇪 德国·移联01');

      // 后台测速触发切换：Auto 策略组的活跃节点变更至 🇸🇬 新加坡·移联02
      final groupsSingapore = [
        Group(
          type: GroupType.Selector,
          name: 'PROXY',
          now: 'Auto',
          all: [_proxy('Auto')],
        ),
        Group(
          type: GroupType.URLTest,
          name: 'Auto',
          now: '🇸🇬 新加坡·移联02',
          all: [_proxy('🇩🇪 德国·移联01'), _proxy('🇸🇬 新加坡·移联02')],
        ),
      ];

      final titleAfter = resolvePhysicalOutboundTitle(
        mode: Mode.rule,
        groups: groupsSingapore,
        selectedMap: const {'PROXY': 'Auto'},
        mainGroupName: 'PROXY',
        fallbackLabel: fallback,
      );
      expect(titleAfter, '🇸🇬 新加坡·移联02');
    });

    test('嵌套故障转移策略组（Fallback）生效时正确解析为有效活跃节点', () {
      final groups = [
        Group(
          type: GroupType.Selector,
          name: 'GLOBAL',
          now: 'Fallback-Group',
          all: [_proxy('Fallback-Group')],
        ),
        Group(
          type: GroupType.Fallback,
          name: 'Fallback-Group',
          now: '🇸🇬 新加坡·移联02',
          all: [_proxy('🇩🇪 德国·移联01'), _proxy('🇸🇬 新加坡·移联02')],
        ),
      ];

      final title = resolvePhysicalOutboundTitle(
        mode: Mode.global,
        groups: groups,
        selectedMap: const {'GLOBAL': 'Fallback-Group'},
        mainGroupName: 'GLOBAL',
        fallbackLabel: fallback,
      );
      expect(title, '🇸🇬 新加坡·移联02');
    });

    test('多级策略组嵌套（Selector -> Selector -> URLTest -> Leaf）深度穿透解析', () {
      final groups = [
        Group(
          type: GroupType.Selector,
          name: 'PROXY',
          now: 'Region-Asia',
          all: [_proxy('Region-Asia')],
        ),
        Group(
          type: GroupType.Selector,
          name: 'Region-Asia',
          now: 'Auto-SG',
          all: [_proxy('Auto-SG')],
        ),
        Group(
          type: GroupType.URLTest,
          name: 'Auto-SG',
          now: '🇸🇬 新加坡·移联02',
          all: [_proxy('🇸🇬 新加坡·移联01'), _proxy('🇸🇬 新加坡·移联02')],
        ),
      ];

      final title = resolvePhysicalOutboundTitle(
        mode: Mode.rule,
        groups: groups,
        selectedMap: const {'PROXY': 'Region-Asia', 'Region-Asia': 'Auto-SG'},
        mainGroupName: 'PROXY',
        fallbackLabel: fallback,
      );
      expect(title, '🇸🇬 新加坡·移联02');
    });

    test('无主策略组且策略组为空时回退至 fallbackLabel', () {
      final title = resolvePhysicalOutboundTitle(
        mode: Mode.rule,
        groups: [],
        selectedMap: const {},
        mainGroupName: null,
        fallbackLabel: 'MyCustomFallback',
      );
      expect(title, 'MyCustomFallback');
    });
  });

  group('Service notificationTitle 同步状态检查', () {
    test('needsNotificationTitleSync 正确防抖和触发同步', () {
      final s = Service();

      // 初始未同步时，任何节点都需要同步
      s.notificationTitle = null;
      expect(s.needsNotificationTitleSync('🇩🇪 德国·移联01'), isTrue);

      // 同步完成后，相同节点不需要重复同步
      s.notificationTitle = '🇩🇪 德国·移联01';
      expect(s.needsNotificationTitleSync('🇩🇪 德国·移联01'), isFalse);

      // 节点发生变动时，需要同步
      expect(s.needsNotificationTitleSync('🇸🇬 新加坡·移联02'), isTrue);

      // 更新到新节点后，新节点无需重复同步
      s.notificationTitle = '🇸🇬 新加坡·移联02';
      expect(s.needsNotificationTitleSync('🇸🇬 新加坡·移联02'), isFalse);

      // 恢复/重置通知后，缓存清空，再次需要同步
      s.notificationTitle = null;
      expect(s.needsNotificationTitleSync('🇸🇬 新加坡·移联02'), isTrue);
    });

    test('updateNotificationNode 保持 notificationTitle 同步并阻止冗余触发', () {
      final s = Service();
      s.notificationTitle = '🇩🇪 德国·移联01';

      s.notificationTitle = '🇸🇬 新加坡·移联02';
      expect(s.notificationTitle, '🇸🇬 新加坡·移联02');
      expect(s.needsNotificationTitleSync('🇸🇬 新加坡·移联02'), isFalse);
      expect(s.needsNotificationTitleSync('🇯🇵 日本·移联03'), isTrue);
    });
  });

  group('GlobalState isScreenOn 状态流转与后台休眠保护', () {
    test('灭屏与亮屏正确更新 isScreenOn 状态', () async {
      globalState.isScreenOn = true;
      expect(globalState.isScreenOn, isTrue);

      // 灭屏
      await globalState.handleScreenStateChanged(false);
      expect(globalState.isScreenOn, isFalse);

      // 亮屏
      await globalState.handleScreenStateChanged(true);
      expect(globalState.isScreenOn, isTrue);

      // 回到前台同样确保 isScreenOn 为 true
      globalState.isScreenOn = false;
      globalState.handleForeground();
      expect(globalState.isScreenOn, isTrue);
    });
  });
}
