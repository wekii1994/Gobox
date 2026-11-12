import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:gobox/clash/clash.dart';
import 'package:gobox/models/models.dart';

// Shared per-container queue so a newly opened stats page waits for any queued
// or in-flight reset/refresh from a recently disposed page instance.
class _FailoverStatsCoordinator {
  Future<void>? _pending;
  int requestId = 0;

  Future<T> serialize<T>(Future<T> Function() action) {
    final prev = _pending;
    final Future<T> current;
    if (prev == null) {
      current = Future<T>.sync(action);
    } else {
      current = prev.then((_) => Future<T>.sync(action));
    }
    late final Future<void> tail;
    tail = current.then<void>(
      (_) {
        if (identical(_pending, tail)) {
          _pending = null;
        }
      },
      onError: (Object _, StackTrace _) {
        if (identical(_pending, tail)) {
          _pending = null;
        }
      },
    );
    _pending = tail;
    return current;
  }
}

final _failoverStatsCoordinatorProvider = Provider<_FailoverStatsCoordinator>(
  (ref) => _FailoverStatsCoordinator(),
);

final failoverStatsProvider =
    AsyncNotifierProvider.autoDispose<FailoverStatsNotifier, FailoverStats>(
      FailoverStatsNotifier.new,
    );

class FailoverStatsNotifier extends AsyncNotifier<FailoverStats> {
  FailoverStatsNotifier({
    Future<FailoverStats> Function()? loadStats,
    Future<FailoverStats> Function()? resetStats,
  }) : _loadStats = loadStats ?? clashCore.getFailoverStats,
       _resetStats = resetStats ?? clashCore.resetFailoverStats;

  final Future<FailoverStats> Function() _loadStats;
  final Future<FailoverStats> Function() _resetStats;

  @override
  Future<FailoverStats> build() {
    final coordinator = ref.read(_failoverStatsCoordinatorProvider);
    ++coordinator.requestId;
    return coordinator.serialize(_loadStats);
  }

  Future<void> refresh() => _run(_loadStats);

  Future<void> reset() => _run(_resetStats);

  Future<void> _run(Future<FailoverStats> Function() action) async {
    if (!ref.mounted) return;
    final coordinator = ref.read(_failoverStatsCoordinatorProvider);
    final requestId = ++coordinator.requestId;
    state = const AsyncValue.loading();
    final next = await coordinator.serialize(() => AsyncValue.guard(action));
    // The page can be popped while native IPC is still in flight. A newer
    // queued action also owns the displayed result once it has been requested.
    if (ref.mounted && requestId == coordinator.requestId) {
      state = next;
    }
  }
}
