import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:gobox/clash/clash.dart';
import 'package:gobox/models/models.dart';

final failoverStatsProvider =
    AsyncNotifierProvider.autoDispose<FailoverStatsNotifier, FailoverStats>(
  FailoverStatsNotifier.new,
);

class FailoverStatsNotifier extends AsyncNotifier<FailoverStats> {
  @override
  Future<FailoverStats> build() async {
    return _fetch();
  }

  Future<FailoverStats> _fetch() async {
    try {
      return await clashCore.getFailoverStats();
    } catch (_) {
      return const FailoverStats();
    }
  }

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    state = await AsyncValue.guard(_fetch);
  }

  Future<void> reset() async {
    state = const AsyncValue.loading();
    state = await AsyncValue.guard(() async {
      return await clashCore.resetFailoverStats();
    });
  }
}
