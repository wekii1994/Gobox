import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:gobox/common/common.dart';
import 'package:gobox/models/models.dart';
import 'package:gobox/providers/providers.dart';
import 'package:gobox/state.dart';
import 'package:gobox/widgets/widgets.dart';
import 'package:material_ui/material_ui.dart';

import 'common.dart';

String _two(int value) => value.toString().padLeft(2, '0');

String _formatDateTime(DateTime dt) =>
    '${dt.year}-${_two(dt.month)}-${_two(dt.day)} ${_two(dt.hour)}:${_two(dt.minute)}:${_two(dt.second)}';

class FailoverStatsPage extends ConsumerWidget {
  const FailoverStatsPage({super.key});

  Future<void> _clear(WidgetRef ref) async {
    final confirmed = await globalState.showMessage(
      title: appLocalizations.failoverStatsClearTitle,
      message: TextSpan(
        text: appLocalizations.failoverStatsClearConfirm,
      ),
    );
    if (confirmed == true) {
      await ref.read(failoverStatsProvider.notifier).reset();
    }
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colorScheme = context.colorScheme;
    final statsAsync = ref.watch(failoverStatsProvider);
    final stats = statsAsync.value ?? const FailoverStats();
    final isLoading = statsAsync.isLoading;
    final hasError = statsAsync.hasError;
    final records = stats.records;

    return CommonScaffold(
      title: appLocalizations.failoverStats,
      actions: [
        IconButton(
          tooltip: appLocalizations.failoverStatsRefresh,
          icon: const Icon(Icons.refresh_rounded),
          onPressed: () => ref.read(failoverStatsProvider.notifier).refresh(),
        ),
        IconButton(
          tooltip: appLocalizations.failoverStatsClear,
          icon: const Icon(Icons.delete_sweep_rounded),
          onPressed: () => _clear(ref),
        ),
      ],
      body: ToolPageBody(
        children: [
          ToolNote(
            icon: Icons.shield_outlined,
            text: appLocalizations.failoverStatsNote,
            color: colorScheme.primary,
          ),
          if (hasError) ...[
            const SizedBox(height: 12),
            ToolNote(
              icon: Icons.error_outline_rounded,
              text: appLocalizations.failoverStatsLoadError,
              color: colorScheme.error,
            ),
          ],
          const SizedBox(height: 12),
          ToolInfoGrid(
            children: [
              ToolInfoTile(
                icon: Icons.today_rounded,
                label: appLocalizations.failoverStatsToday,
                value: '${stats.today}',
                color: const Color(0xFF2F80ED),
              ),
              ToolInfoTile(
                icon: Icons.history_rounded,
                label: appLocalizations.failoverStatsTotal,
                value: '${stats.total}',
                color: const Color(0xFF7B61FF),
              ),
            ],
          ),
          const SizedBox(height: 16),
          ToolSectionHeader(
            appLocalizations.failoverStatsHistory,
            trailing: records.isNotEmpty
                ? Text(
                    '${records.length}',
                    style: context.textTheme.labelMedium?.copyWith(
                      color: colorScheme.onSurfaceVariant,
                    ),
                  )
                : null,
          ),
          ToolLoadingSwitcher(
            loading: isLoading,
            child: records.isEmpty
                ? ToolSurface(
                    padding: const EdgeInsets.symmetric(
                      vertical: 36,
                      horizontal: 16,
                    ),
                    child: Center(
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Icon(
                            Icons.check_circle_outline_rounded,
                            size: 40,
                            color: colorScheme.primary.withValues(alpha: 0.6),
                          ),
                          const SizedBox(height: 10),
                          Text(
                            appLocalizations.failoverStatsEmpty,
                            style: context.textTheme.bodyMedium?.copyWith(
                              color: colorScheme.onSurfaceVariant,
                            ),
                          ),
                          const SizedBox(height: 4),
                          Text(
                            appLocalizations.failoverStatsEmptyDesc,
                            style: context.textTheme.bodySmall?.copyWith(
                              color: colorScheme.onSurfaceVariant
                                  .withValues(alpha: 0.7),
                            ),
                            textAlign: TextAlign.center,
                          ),
                        ],
                      ),
                    ),
                  )
                : Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      for (final record in records) ...[
                        _FailoverRecordCard(record: record),
                        const SizedBox(height: 8),
                      ],
                    ],
                  ),
          ),
        ],
      ),
    );
  }
}

class _FailoverRecordCard extends StatelessWidget {
  final FailoverRecord record;

  const _FailoverRecordCard({
    required this.record,
  });

  @override
  Widget build(BuildContext context) {
    final colorScheme = context.colorScheme;
    final timeStr = _formatDateTime(record.dateTime);

    return ToolSurface(
      padding: const EdgeInsets.all(14),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                decoration: BoxDecoration(
                  color: colorScheme.primaryContainer,
                  borderRadius: BorderRadius.circular(6),
                ),
                child: Text(
                  record.group.isEmpty ? 'ProxyGroup' : record.group,
                  style: context.textTheme.labelSmall?.copyWith(
                    color: colorScheme.onPrimaryContainer,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              const Spacer(),
              Text(
                timeStr,
                style: context.textTheme.bodySmall?.copyWith(
                  color: colorScheme.onSurfaceVariant,
                ),
              ),
            ],
          ),
          const SizedBox(height: 10),
          Row(
            children: [
              Expanded(
                child: Row(
                  children: [
                    Icon(
                      Icons.cancel_outlined,
                      size: 16,
                      color: colorScheme.error,
                    ),
                    const SizedBox(width: 6),
                    Expanded(
                      child: Text(
                        record.before.isEmpty ? '—' : record.before,
                        style: context.textTheme.bodyMedium?.copyWith(
                          color: colorScheme.onSurfaceVariant,
                          decoration: TextDecoration.lineThrough,
                        ),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                  ],
                ),
              ),
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 8),
                child: Icon(
                  Icons.arrow_forward_rounded,
                  size: 16,
                  color: colorScheme.onSurfaceVariant,
                ),
              ),
              Expanded(
                child: Row(
                  children: [
                    const Icon(
                      Icons.check_circle_rounded,
                      size: 16,
                      color: Color(0xFF3B9C4A),
                    ),
                    const SizedBox(width: 6),
                    Expanded(
                      child: Text(
                        record.after.isEmpty ? '—' : record.after,
                        style: context.textTheme.bodyMedium?.copyWith(
                          fontWeight: FontWeight.w600,
                          color: const Color(0xFF3B9C4A),
                        ),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}
