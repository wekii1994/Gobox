import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:gobox/clash/clash.dart';
import 'package:gobox/models/models.dart';

final failoverStatsProvider =
    AsyncNotifierProvider.autoDispose<FailoverStatsNotifier, FailoverStats>(
      FailoverStatsNotifier.new,
    );

class FailoverStatsNotifier extends AsyncNotifier<FailoverStats> {
  @override
  Future<FailoverStats> build() => clashCore.getFailoverStats();

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    state = await AsyncValue.guard(clashCore.getFailoverStats);
  }

  Future<void> reset() async {
    state = const AsyncValue.loading();
    state = await AsyncValue.guard(clashCore.resetFailoverStats);
  }
}
